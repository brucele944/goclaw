package pg

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

// Scan types for statements shared by the PostgreSQL build and the SQLite/lite
// build — the export queries take a *sql.DB and run on both backends.
// PostgreSQL hands back timestamptz as time.Time and text[] as a "{a,b}" literal;
// SQLite returns the TEXT it stored. These types accept either representation so
// one row struct decodes on both.

// archiveTimeLayouts are the timestamp encodings the two backends can store.
var archiveTimeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

// normalizeArchiveTime rewrites a timestamp read from either backend into the
// archive's RFC3339 UTC seconds form. It replaces the PostgreSQL-only to_char()
// projection the export queries used, which SQLite cannot execute. Unrecognized
// input is passed through unchanged rather than dropped.
func normalizeArchiveTime(s string) string {
	if strings.TrimSpace(s) == "" {
		return s
	}
	for _, layout := range archiveTimeLayouts {
		if parsed, err := time.Parse(layout, s); err == nil {
			return parsed.UTC().Format("2006-01-02T15:04:05Z")
		}
	}
	return s
}

// pgTime scans a timestamp column that PostgreSQL returns as time.Time and
// SQLite as TEXT.
type pgTime struct{ time.Time }

// Scan implements sql.Scanner.
func (t *pgTime) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		t.Time = time.Time{}
		return nil
	case time.Time:
		t.Time = v
		return nil
	case []byte:
		return t.parse(string(v))
	case string:
		return t.parse(v)
	default:
		return fmt.Errorf("pgTime: unsupported type %T", src)
	}
}

func (t *pgTime) parse(s string) error {
	for _, layout := range archiveTimeLayouts {
		if parsed, err := time.Parse(layout, s); err == nil {
			t.Time = parsed
			return nil
		}
	}
	return fmt.Errorf("pgTime: cannot parse %q", s)
}

// TimePtr returns the parsed time, or nil when the column was NULL. Callers use
// it to fill *time.Time fields from a tolerant scan.
func (t *pgTime) TimePtr() *time.Time {
	if t == nil {
		return nil
	}
	parsed := t.Time
	return &parsed
}

// ExportStringArray scans a text[] column that PostgreSQL returns as an array
// literal ("{a,b}") and SQLite as the JSON array it stored.
type ExportStringArray []string

// Scan implements sql.Scanner.
func (a *ExportStringArray) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*a = nil
		return nil
	case []byte:
		return a.parse(string(v))
	case string:
		return a.parse(v)
	default:
		return fmt.Errorf("ExportStringArray: unsupported type %T", src)
	}
}

func (a *ExportStringArray) parse(s string) error {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		*a = nil
		return nil
	}
	if strings.HasPrefix(trimmed, "{") {
		return pq.Array((*[]string)(a)).Scan(trimmed)
	}
	var out []string
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return fmt.Errorf("ExportStringArray: cannot parse %q: %w", trimmed, err)
	}
	*a = out
	return nil
}

// normalizeTimePtr normalizes an optional archive timestamp in place.
func normalizeTimePtr(s *string) {
	if s == nil {
		return
	}
	*s = normalizeArchiveTime(*s)
}

// ExportJSON scans a JSON column. PostgreSQL returns jsonb as []byte; SQLite
// returns the TEXT it stored, and database/sql cannot scan that into
// jsontext.Value (json.RawMessage) — the driver hands back a string.
type ExportJSON json.RawMessage

// Scan implements sql.Scanner.
func (j *ExportJSON) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*j = nil
		return nil
	case []byte:
		*j = append((*j)[:0], v...)
		return nil
	case string:
		*j = append((*j)[:0], v...)
		return nil
	default:
		return fmt.Errorf("ExportJSON: unsupported type %T", src)
	}
}

// MarshalJSON and UnmarshalJSON delegate to json.RawMessage so archive payloads
// keep their JSON shape — a bare []byte-kind type would marshal as base64.
func (j ExportJSON) MarshalJSON() ([]byte, error) { return json.RawMessage(j).MarshalJSON() }

func (j *ExportJSON) UnmarshalJSON(data []byte) error {
	return (*json.RawMessage)(j).UnmarshalJSON(data)
}
