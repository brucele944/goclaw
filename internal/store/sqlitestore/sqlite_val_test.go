//go:build sqlite || sqliteonly

package sqlitestore

import (
	"encoding/json"
	"testing"
)

// sqliteVal must strip U+0000 from text exactly like the PostgreSQL dialect
// does, so both engines persist the same bytes (see base.StripNUL). Plain
// []byte stays untouched: it is binary data, not text.
func TestSQLiteVal_StripsNULFromText(t *testing.T) {
	if got := sqliteVal("a\x00b"); got != "ab" {
		t.Errorf("sqliteVal(string) = %v, want \"ab\"", got)
	}

	raw, ok := sqliteVal(json.RawMessage(`{"k":"a\u0000b"}`)).(json.RawMessage)
	if !ok {
		t.Fatalf("sqliteVal(json.RawMessage) type = %T, want json.RawMessage", sqliteVal(json.RawMessage(`{}`)))
	}
	if want := `{"k":"ab"}`; string(raw) != want {
		t.Errorf("sqliteVal(json.RawMessage) = %s, want %s", raw, want)
	}

	mapped, ok := sqliteVal(map[string]string{"k": "a\x00b"}).(string)
	if !ok {
		t.Fatalf("sqliteVal(map) type = %T, want string", sqliteVal(map[string]string{}))
	}
	// json.Marshal escapes U+0000 as \u0000; the escape must be gone too.
	if want := `{"k":"ab"}`; mapped != want {
		t.Errorf("sqliteVal(map) = %s, want %s", mapped, want)
	}
}

func TestSQLiteVal_KeepsBinaryAndScalars(t *testing.T) {
	bin := []byte{1, 0, 2}
	got, ok := sqliteVal(bin).([]byte)
	if !ok || string(got) != string(bin) {
		t.Errorf("sqliteVal([]byte) = %v, want binary unchanged", got)
	}

	if got := sqliteVal(42); got != 42 {
		t.Errorf("sqliteVal(int) = %v, want 42", got)
	}
	if got := sqliteVal(nil); got != nil {
		t.Errorf("sqliteVal(nil) = %v, want nil", got)
	}
}
