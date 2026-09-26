package store

import (
	"encoding/json"
	"testing"
)

// TestParseModelRoles covers the role → "provider/model" on-disk shape: valid
// entries survive, malformed ones are dropped without discarding the whole map.
func TestParseModelRoles(t *testing.T) {
	ag := &AgentData{
		ModelRoles: json.RawMessage(`{
			"coder": "openai/gpt-5",
			"summarizer": "anthropic/claude-haiku-4-5",
			"router_model": "together/meta-llama/Llama-3-70b",
			"  ": "openai/x",
			"bad name!": "openai/y",
			"noprovider": "/gpt-5",
			"nomodel": "openai/",
			"empty": ""
		}`),
	}

	roles := ag.ParseModelRoles()
	if len(roles) != 3 {
		t.Fatalf("roles = %+v, want only the 3 well-formed entries", roles)
	}
	if got := roles["coder"]; got.Provider != "openai" || got.Model != "gpt-5" {
		t.Fatalf("coder = %+v, want openai/gpt-5", got)
	}
	if got := roles["summarizer"]; got.Provider != "anthropic" || got.Model != "claude-haiku-4-5" {
		t.Fatalf("summarizer = %+v, want anthropic/claude-haiku-4-5", got)
	}
	// The split is on the first slash so a model id containing slashes survives.
	if got := roles["router_model"]; got.Provider != "together" || got.Model != "meta-llama/Llama-3-70b" {
		t.Fatalf("router_model = %+v, want together/meta-llama/Llama-3-70b", got)
	}
}

func TestParseModelRolesEmptyAndNil(t *testing.T) {
	cases := map[string]json.RawMessage{
		"nil":            nil,
		"empty object":   json.RawMessage(`{}`),
		"null":           json.RawMessage(`null`),
		"not an object":  json.RawMessage(`["coder"]`),
		"string values?": json.RawMessage(`{"coder":{"provider":"openai"}}`),
	}
	for name, raw := range cases {
		ag := &AgentData{ModelRoles: raw}
		if roles := ag.ParseModelRoles(); roles != nil {
			t.Fatalf("%s: roles = %+v, want nil", name, roles)
		}
	}
	var nilAgent *AgentData
	if roles := nilAgent.ParseModelRoles(); roles != nil {
		t.Fatalf("nil agent: roles = %+v, want nil", roles)
	}
}

func TestValidModelRoleName(t *testing.T) {
	for _, ok := range []string{"coder", "summarizer", "fast.2", "a_b-c"} {
		if !ValidModelRoleName(ok) {
			t.Errorf("ValidModelRoleName(%q) = false, want true", ok)
		}
	}
	long := make([]byte, ModelRolesKeyMaxLen+1)
	for i := range long {
		long[i] = 'a'
	}
	for _, bad := range []string{"", "has space", "emoji🙂", "colon:key", "slash/key", string(long)} {
		if ValidModelRoleName(bad) {
			t.Errorf("ValidModelRoleName(%q) = true, want false", bad)
		}
	}
}
