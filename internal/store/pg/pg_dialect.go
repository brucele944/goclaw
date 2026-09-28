package pg

import (
	"encoding/json"
	"fmt"

	"github.com/nextlevelbuilder/goclaw/internal/store/base"
)

// pgDialect implements base.Dialect for PostgreSQL ($1, $2, ... placeholders).
var pgDialect base.Dialect = pgDialectImpl{}

type pgDialectImpl struct{}

func (pgDialectImpl) Placeholder(n int) string { return fmt.Sprintf("$%d", n) }

// TransformValue strips U+0000 from text values so a dynamic UPDATE cannot fail
// with SQLSTATE 22021 just because a tool result or LLM answer carried a NUL.
// Plain []byte stays untouched: it is binary data (bytea), not text.
func (pgDialectImpl) TransformValue(v any) any {
	switch typed := v.(type) {
	case string:
		return base.StripNUL(typed)
	case *string:
		if typed == nil {
			return nil
		}
		s := base.StripNUL(*typed)
		return &s
	case json.RawMessage:
		return json.RawMessage(base.StripNULJSON(typed))
	}
	return v
}

func (pgDialectImpl) SupportsReturning() bool { return true }
