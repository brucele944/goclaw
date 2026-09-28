package base

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
)

// --- NUL stripping ---
// PostgreSQL cannot store U+0000 in a text column (SQLSTATE 22021) or in jsonb
// (22021 for the raw byte, 22P05 for the \u0000 escape). Tool output, file bytes
// and LLM answers all reach the stores verbatim, so the write boundary is the
// only place that can guarantee a storable value. SQLite strips identically, so
// both dialects persist the same bytes.

// nulEscape is the lower-case JSON escape for U+0000.
var nulEscape = []byte("u0000")

// StripNUL removes U+0000 from s, returning s itself (no copy) when clean.
func StripNUL(s string) string {
	if strings.IndexByte(s, 0) < 0 {
		return s
	}
	return strings.ReplaceAll(s, "\x00", "")
}

// StripNULBytes removes U+0000 bytes from data, returning data itself when clean.
func StripNULBytes(data []byte) []byte {
	if bytes.IndexByte(data, 0) < 0 {
		return data
	}
	return bytes.ReplaceAll(data, []byte{0}, nil)
}

// StripNULJSON removes U+0000 from JSON text: both the raw byte and the escaped
// \u0000 form that jsonb rejects. A backslash-escaped backslash (\\u0000) is
// data and is left alone. Returns data itself when clean.
func StripNULJSON(data []byte) []byte {
	if bytes.IndexByte(data, 0) < 0 && !bytes.Contains(data, nulEscape) {
		return data
	}
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); {
		switch {
		case data[i] == 0:
			i++
		case data[i] == '\\':
			j := i
			for j < len(data) && data[j] == '\\' {
				j++
			}
			// An odd run of backslashes means what follows is an escape
			// sequence: \u0000 there is the NUL escape, so both it and the
			// backslash introducing it must go. An even run leaves the
			// following characters as literal data.
			if (j-i)%2 == 1 && j+len(nulEscape) <= len(data) && bytes.Equal(data[j:j+len(nulEscape)], nulEscape) {
				out = append(out, data[i:j-1]...)
				j += len(nulEscape)
			} else {
				out = append(out, data[i:j]...)
			}
			i = j
		default:
			out = append(out, data[i])
			i++
		}
	}
	return out
}

// --- Nullable helpers ---
// Convert Go zero values to nil pointers for nullable DB columns.

// NilStr returns nil for empty strings, pointer otherwise. U+0000 is stripped
// first (see StripNUL) so a NUL-only value becomes NULL instead of a byte
// sequence the database rejects.
func NilStr(s string) *string {
	s = StripNUL(s)
	if s == "" {
		return nil
	}
	return &s
}

// NilInt returns nil for zero, pointer otherwise.
func NilInt(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}

// NilUUID returns nil for uuid.Nil, pointer otherwise.
func NilUUID(u *uuid.UUID) *uuid.UUID {
	if u == nil || *u == uuid.Nil {
		return nil
	}
	return u
}

// NilTime returns nil for nil/zero time, pointer otherwise.
func NilTime(t *time.Time) *time.Time {
	if t == nil || t.IsZero() {
		return nil
	}
	return t
}

// --- Deref helpers ---
// Safely dereference nullable pointers with zero-value defaults.

// DerefStr returns "" for nil, value otherwise.
func DerefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// DerefInt returns 0 for nil, value otherwise.
func DerefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// DerefUUID returns uuid.Nil for nil, value otherwise.
func DerefUUID(u *uuid.UUID) uuid.UUID {
	if u == nil {
		return uuid.Nil
	}
	return *u
}

// DerefBytes returns nil for nil, value otherwise.
func DerefBytes(b *[]byte) []byte {
	if b == nil {
		return nil
	}
	return *b
}

// --- JSON helpers ---
// Handle nullable JSON columns with safe defaults.

// JsonOrEmpty returns "{}" for nil, data with U+0000 stripped otherwise.
func JsonOrEmpty(data []byte) []byte {
	if data == nil {
		return []byte("{}")
	}
	return StripNULJSON(data)
}

// JsonOrEmptyArray returns "[]" for nil, data with U+0000 stripped otherwise.
func JsonOrEmptyArray(data []byte) []byte {
	if data == nil {
		return []byte("[]")
	}
	return StripNULJSON(data)
}

// JsonOrNull returns nil for nil RawMessage, U+0000-stripped []byte otherwise.
func JsonOrNull(data json.RawMessage) any {
	if data == nil {
		return nil
	}
	return StripNULJSON([]byte(data))
}
