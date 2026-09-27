//go:build sqlite || sqliteonly

package sqlitestore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"log/slog"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver
)

// connectionPragmas are applied to EVERY new SQLite connection.
// Critical: PRAGMA settings are per-connection in SQLite. Using db.Exec()
// only applies to ONE connection in the pool — other connections won't have
// busy_timeout, causing immediate SQLITE_BUSY errors under concurrency.
var connectionPragmas = []string{
	"PRAGMA journal_mode = WAL",
	"PRAGMA busy_timeout = 15000",
	"PRAGMA synchronous = NORMAL",
	"PRAGMA cache_size = -8000", // 8MB cache
	"PRAGMA foreign_keys = ON",
}

// pragmaConnector wraps a sql.Driver to apply PRAGMAs on every new connection.
// This ensures ALL connections in the pool have busy_timeout, WAL mode, etc.
type pragmaConnector struct {
	driver  driver.Driver
	dsn     string
	pragmas []string
}

func (c *pragmaConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.driver.Open(c.dsn)
	if err != nil {
		return nil, err
	}

	// Apply PRAGMAs to this specific connection.
	for _, p := range c.pragmas {
		if execer, ok := conn.(driver.ExecerContext); ok {
			if _, execErr := execer.ExecContext(ctx, p, nil); execErr != nil {
				slog.Warn("sqlite pragma failed on new conn", "pragma", p, "error", execErr)
			}
		} else if execer, ok := conn.(driver.Execer); ok { //nolint:staticcheck
			if _, execErr := execer.Exec(p, nil); execErr != nil {
				slog.Warn("sqlite pragma failed on new conn", "pragma", p, "error", execErr)
			}
		}
	}
	return &timeNormalizingConn{Conn: conn}, nil
}

// sqliteTimestampLayout is the canonical text format for time columns: the same
// shape the schema's DDL defaults write (`strftime('%Y-%m-%dT%H:%M:%fZ','now')`),
// in UTC with millisecond precision.
const sqliteTimestampLayout = "2006-01-02T15:04:05.000Z"

// timeNormalizingConn rewrites bound time.Time values to sqliteTimestampLayout.
// Without it the driver writes time.Time.String() — "2026-09-27 06:13:53.08
// +0000 UTC m=+8.334" — while the DDL defaults write "2026-09-27T06:13:53.080Z".
// SQLite compares TEXT lexically, so ' ' (0x20) sorts before 'T' (0x54) and rows
// written through a bind compare as older than rows written by a default,
// breaking range filters (`created_at >= ?`) and ORDER BY on mixed columns.
type timeNormalizingConn struct {
	driver.Conn
}

// CheckNamedValue normalizes time.Time arguments — including the *time.Time and
// sql.NullTime shapes AGENTS.md mandates for nullable timestamp columns, and any
// other driver.Valuer that resolves to a time.Time — and defers every other value
// to the database/sql default conversion. A bare type assertion on nv.Value would
// miss all of those: database/sql's DefaultParameterConverter dereferences a
// *time.Time or calls Value() on a Valuer BEFORE the driver ever sees it, so
// running the same conversion here first is what makes the check exhaustive.
func (c *timeNormalizingConn) CheckNamedValue(nv *driver.NamedValue) error {
	v, err := driver.DefaultParameterConverter.ConvertValue(nv.Value)
	if err != nil {
		return driver.ErrSkip
	}
	ts, ok := v.(time.Time)
	if !ok {
		return driver.ErrSkip
	}
	nv.Value = ts.UTC().Format(sqliteTimestampLayout)
	return nil
}

// Pass the connection's optional capabilities through, so wrapping does not
// change how database/sql drives the connection (context cancellation, session
// reset, validation, ping, direct exec/query).

func (c *timeNormalizingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if execer, ok := c.Conn.(driver.ExecerContext); ok {
		return execer.ExecContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}

func (c *timeNormalizingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if queryer, ok := c.Conn.(driver.QueryerContext); ok {
		return queryer.QueryContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}

func (c *timeNormalizingConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if beginner, ok := c.Conn.(driver.ConnBeginTx); ok {
		return beginner.BeginTx(ctx, opts)
	}
	return nil, driver.ErrSkip
}

func (c *timeNormalizingConn) ResetSession(ctx context.Context) error {
	if resetter, ok := c.Conn.(driver.SessionResetter); ok {
		return resetter.ResetSession(ctx)
	}
	return nil
}

func (c *timeNormalizingConn) IsValid() bool {
	if validator, ok := c.Conn.(driver.Validator); ok {
		return validator.IsValid()
	}
	return true
}

func (c *timeNormalizingConn) Ping(ctx context.Context) error {
	if pinger, ok := c.Conn.(driver.Pinger); ok {
		return pinger.Ping(ctx)
	}
	return nil
}

func (c *pragmaConnector) Driver() driver.Driver { return c.driver }

// OpenDB opens a SQLite database at the given path with WAL mode and recommended pragmas.
// Uses modernc.org/sqlite (pure Go, zero CGo).
//
// Desktop app concurrency model:
// - WAL mode: allows concurrent readers alongside a single writer
// - busy_timeout=15000: ALL connections wait up to 15s before SQLITE_BUSY
// - _txlock=immediate: write transactions acquire lock immediately (fail-fast on contention)
//
// PRAGMAs are applied per-connection via pragmaConnector, ensuring every
// connection in the pool has consistent settings (busy_timeout, WAL, etc.).
func OpenDB(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_txlock=immediate", path)

	// Get the registered driver to wrap with pragmaConnector.
	drv, err := getSQLiteDriver()
	if err != nil {
		return nil, err
	}

	db := sql.OpenDB(&pragmaConnector{
		driver:  drv,
		dsn:     dsn,
		pragmas: connectionPragmas,
	})

	// SQLite is single-writer; WAL allows concurrent readers.
	// 4 connections: up to 3 readers + 1 writer can proceed in parallel,
	// reducing connection pool starvation during concurrent operations.
	db.SetMaxOpenConns(4)

	// Verify connection works (also triggers first pragma application).
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	return db, nil
}

// getSQLiteDriver retrieves the registered "sqlite" driver instance.
func getSQLiteDriver() (driver.Driver, error) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("get sqlite driver: %w", err)
	}
	drv := db.Driver()
	db.Close()
	return drv, nil
}
