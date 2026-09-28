package pg

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/store/base"
)

func TestPGDialect_Placeholder(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{1, "$1"},
		{2, "$2"},
		{5, "$5"},
		{10, "$10"},
	}
	for _, tt := range tests {
		got := pgDialect.Placeholder(tt.n)
		if got != tt.want {
			t.Errorf("Placeholder(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestPGDialect_TransformValue(t *testing.T) {
	tests := []struct {
		name  string
		input any
	}{
		{"string", "hello"},
		{"int", 42},
		{"float", 3.14},
		{"nil", nil},
	}
	for _, tt := range tests {
		got := pgDialect.TransformValue(tt.input)
		// PG dialect returns identity for all values
		if got != tt.input {
			t.Errorf("TransformValue(%v) = %v, want identity", tt.input, got)
		}
	}

	// Test that maps are returned as-is (identity transform)
	testMap := map[string]string{"key": "value"}
	gotMap := pgDialect.TransformValue(testMap)
	if !reflect.DeepEqual(gotMap, testMap) {
		t.Error("TransformValue should return map identity")
	}

	// Test that slices are returned as-is (identity transform)
	testSlice := []string{"a", "b"}
	gotSlice := pgDialect.TransformValue(testSlice)
	if !reflect.DeepEqual(gotSlice, testSlice) {
		t.Error("TransformValue should return slice identity")
	}
}

// U+0000 cannot be stored in a PostgreSQL text column (SQLSTATE 22021) or in
// jsonb (22021 raw byte, 22P05 \u0000 escape), so the dialect strips text
// values on the way to the driver and leaves binary data alone.
func TestPGDialect_TransformValue_StripsNUL(t *testing.T) {
	if got := pgDialect.TransformValue("a\x00b"); got != "ab" {
		t.Errorf("TransformValue(text) = %v, want \"ab\"", got)
	}

	s := "a\x00b"
	gotPtr := pgDialect.TransformValue(&s)
	ptr, ok := gotPtr.(*string)
	if !ok || ptr == nil {
		t.Fatalf("TransformValue(*string) = %#v, want non-nil *string", gotPtr)
	}
	if *ptr != "ab" {
		t.Errorf("*TransformValue(*string) = %q, want \"ab\"", *ptr)
	}
	var nilPtr *string
	if got := pgDialect.TransformValue(nilPtr); got != nil {
		t.Errorf("TransformValue(nil *string) = %v, want nil", got)
	}

	raw, ok := pgDialect.TransformValue(json.RawMessage(`{"k":"a\u0000b"}`)).(json.RawMessage)
	if !ok {
		t.Fatalf("TransformValue(json.RawMessage) type = %T, want json.RawMessage", pgDialect.TransformValue(json.RawMessage(`{}`)))
	}
	if want := `{"k":"ab"}`; string(raw) != want {
		t.Errorf("TransformValue(json.RawMessage) = %s, want %s", raw, want)
	}

	bin := []byte{1, 0, 2}
	gotBin, ok := pgDialect.TransformValue(bin).([]byte)
	if !ok || !bytes.Equal(gotBin, bin) {
		t.Errorf("TransformValue([]byte) = %v, want binary unchanged", gotBin)
	}
}

func TestPGDialect_SupportsReturning(t *testing.T) {
	if !pgDialect.SupportsReturning() {
		t.Errorf("SupportsReturning() = false, want true")
	}
}

func TestPGDialect_ImplementsInterface(t *testing.T) {
	var _ base.Dialect = pgDialect
}
