package store

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nextlevelbuilder/goclaw/internal/i18n"
	"github.com/nextlevelbuilder/goclaw/internal/providers/wire"
)

func TestParseThinkingEnabled(t *testing.T) {
	trueVal := true
	falseVal := false

	tests := []struct {
		name     string
		settings json.RawMessage
		want     *bool
	}{
		{
			name:     "unset settings",
			settings: nil,
			want:     nil,
		},
		{
			name:     "empty object",
			settings: json.RawMessage(`{}`),
			want:     nil,
		},
		{
			name:     "explicit true",
			settings: json.RawMessage(`{"thinking_enabled":true}`),
			want:     &trueVal,
		},
		{
			name:     "explicit false",
			settings: json.RawMessage(`{"thinking_enabled":false}`),
			want:     &falseVal,
		},
		{
			name:     "coexists with other settings keys",
			settings: json.RawMessage(`{"num_ctx":8192,"thinking_enabled":true}`),
			want:     &trueVal,
		},
		{
			name:     "malformed json",
			settings: json.RawMessage(`{not valid json`),
			want:     nil,
		},
		{
			name:     "wrong type is ignored",
			settings: json.RawMessage(`{"thinking_enabled":"yes"}`),
			want:     nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseThinkingEnabled(tc.settings)
			if tc.want == nil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, *tc.want, *got)
		})
	}
}

func TestValidateProviderDeclaration(t *testing.T) {
	t.Run("wire_api accepts every declared protocol", func(t *testing.T) {
		for wireAPI := range ValidWireAPIs {
			assert.NoError(t, ValidateWireAPI(wireAPI), "wire_api %q must be accepted", wireAPI)
		}
	})

	t.Run("wire_api rejects unknown and empty values", func(t *testing.T) {
		for _, wireAPI := range []string{"", "openai", "anthropic", "openai-completions "} {
			err := ValidateWireAPI(wireAPI)
			require.Error(t, err, "wire_api %q must be rejected", wireAPI)
			// i18n-keyed: the message is localized text, never the bare key.
			assert.NotEqual(t, i18n.MsgProviderInvalidWireAPI, err.Error())
			assert.Contains(t, err.Error(), "openai-completions")
		}
	})

	t.Run("auth_kind accepts every declared shape", func(t *testing.T) {
		for authKind := range ValidAuthKinds {
			assert.NoError(t, ValidateAuthKind(authKind), "auth_kind %q must be accepted", authKind)
		}
	})

	t.Run("auth_kind rejects unknown and empty values", func(t *testing.T) {
		for _, authKind := range []string{"", "api-key", "oauth"} {
			err := ValidateAuthKind(authKind)
			require.Error(t, err, "auth_kind %q must be rejected", authKind)
			assert.NotEqual(t, i18n.MsgProviderInvalidAuthKind, err.Error())
			assert.Contains(t, err.Error(), "api_key")
		}
	})

	t.Run("settings_version rejects versions this build cannot decode", func(t *testing.T) {
		assert.NoError(t, ValidateSettingsVersion(CurrentSettingsVersion))
		for _, version := range []int{0, -1, CurrentSettingsVersion + 1, 99} {
			err := ValidateSettingsVersion(version)
			require.Error(t, err, "settings_version %d must be rejected", version)
			assert.NotEqual(t, i18n.MsgProviderInvalidSettingsVersion, err.Error())
		}
	})

	t.Run("normalize derives the declaration from the provider type", func(t *testing.T) {
		// A blank wire_api means "the caller did not say", so it comes from the
		// brand — not from an OpenAI-compatible default, which would hand a
		// non-OpenAI provider to the wrong transport. A type with no brand is
		// refused instead of guessed.
		assert.Error(t, NormalizeProviderDeclaration(&LLMProviderData{}))

		p := &LLMProviderData{ProviderType: ProviderOllama}
		require.NoError(t, NormalizeProviderDeclaration(p))
		assert.Equal(t, WireAPIOllamaNative, p.WireAPI)
		assert.Equal(t, AuthKindNone, p.AuthKind)
		assert.Equal(t, CurrentSettingsVersion, p.SettingsVersion)

		// A fully declared row keeps what it states, brand or not.
		explicit := &LLMProviderData{ProviderType: ProviderAnthropicNative, WireAPI: WireAPICLIDelegated, AuthKind: AuthKindCLIDelegated, SettingsVersion: 1}
		require.NoError(t, NormalizeProviderDeclaration(explicit))
		assert.Equal(t, WireAPICLIDelegated, explicit.WireAPI)
		assert.Equal(t, AuthKindCLIDelegated, explicit.AuthKind)

		assert.Error(t, NormalizeProviderDeclaration(&LLMProviderData{WireAPI: "nope"}))
		assert.Error(t, NormalizeProviderDeclaration(&LLMProviderData{AuthKind: "nope"}))
		assert.Error(t, NormalizeProviderDeclaration(&LLMProviderData{SettingsVersion: 7}))
	})

	t.Run("provider updates validate only the keys present", func(t *testing.T) {
		assert.NoError(t, ValidateProviderUpdates(nil))
		assert.NoError(t, ValidateProviderUpdates(map[string]any{"display_name": "x"}))
		assert.NoError(t, ValidateProviderUpdates(map[string]any{"wire_api": WireAPIOllamaNative}))
		assert.NoError(t, ValidateProviderUpdates(map[string]any{"auth_kind": AuthKindNone}))
		// JSON-decoded updates carry float64, Go-built ones int/int64.
		assert.NoError(t, ValidateProviderUpdates(map[string]any{"settings_version": float64(1)}))
		assert.NoError(t, ValidateProviderUpdates(map[string]any{"settings_version": 1}))
		assert.NoError(t, ValidateProviderUpdates(map[string]any{"settings_version": int64(1)}))

		assert.Error(t, ValidateProviderUpdates(map[string]any{"wire_api": "nope"}))
		assert.Error(t, ValidateProviderUpdates(map[string]any{"wire_api": 42}))
		assert.Error(t, ValidateProviderUpdates(map[string]any{"auth_kind": "nope"}))
		assert.Error(t, ValidateProviderUpdates(map[string]any{"auth_kind": nil}))
		assert.Error(t, ValidateProviderUpdates(map[string]any{"settings_version": 2}))
		assert.Error(t, ValidateProviderUpdates(map[string]any{"settings_version": "1"}))
		assert.Error(t, ValidateProviderUpdates(map[string]any{"settings_version": 1.5}))
	})
}

func TestFillModelDefaults(t *testing.T) {
	custom := &LLMModel{
		Modalities:   json.RawMessage(`["text","image"]`),
		Capabilities: json.RawMessage(`{"vision":true}`),
		Reasoning:    json.RawMessage(`{"efforts":["low","high"]}`),
		Compat:       json.RawMessage(`{"drop_reasoning":true}`),
		Source:       ModelSourceOperator,
	}
	FillModelDefaults(custom)
	assert.Equal(t, `["text","image"]`, string(custom.Modalities))
	assert.Equal(t, `{"vision":true}`, string(custom.Capabilities))
	assert.Equal(t, `{"efforts":["low","high"]}`, string(custom.Reasoning))
	assert.Equal(t, `{"drop_reasoning":true}`, string(custom.Compat))
	assert.Equal(t, ModelSourceOperator, custom.Source)

	empty := &LLMModel{}
	FillModelDefaults(empty)
	assert.Equal(t, `["text"]`, string(empty.Modalities))
	assert.Equal(t, `{}`, string(empty.Capabilities))
	assert.Equal(t, `{}`, string(empty.Reasoning))
	assert.Equal(t, `{}`, string(empty.Compat))
	assert.Equal(t, ModelSourceBundled, empty.Source)
}

func TestQualifiedModelColumns(t *testing.T) {
	qualified := QualifiedModelColumns("m")
	for _, column := range strings.Split(LLMModelColumns, ", ") {
		assert.Contains(t, qualified, "m."+column, "column %q must be qualified", column)
	}
	assert.Equal(t, len(strings.Split(LLMModelColumns, ", ")), strings.Count(qualified, "m."))
}

// TestDeclarationEnumsMatchWireRegistry pins the store's declaration constants to
// the wire dispatch registry: the values the store validates and the values the
// dispatcher can route must be the same set, with no hand-maintained copies.
func TestDeclarationEnumsMatchWireRegistry(t *testing.T) {
	assert.Equal(t, wire.ValidAPIs(), ValidWireAPIs)
	assert.Equal(t, wire.ValidAuthKinds(), ValidAuthKinds)

	registered := wire.All()
	require.Len(t, registered, len(ValidWireAPIs))
	for _, d := range registered {
		assert.True(t, ValidWireAPIs[string(d.API)], "descriptor %s must be an accepted wire_api", d.API)
		assert.True(t, ValidAuthKinds[string(d.AuthKind)], "descriptor %s declares auth kind %s", d.API, d.AuthKind)
	}
	for value := range ValidWireAPIs {
		_, ok := wire.Lookup(wire.API(value))
		assert.True(t, ok, "declared wire_api %q has no registered transport", value)
	}
}

// TestEveryProviderTypeHasAWireBrand keeps the brand catalog and the accepted
// provider_type list from drifting: a provider type with no brand entry would
// silently lose its vendor base URL, default model and transport quirks.
func TestEveryProviderTypeHasAWireBrand(t *testing.T) {
	for providerType := range ValidProviderTypes {
		brand, ok := wire.BrandFor(providerType)
		if !ok {
			t.Errorf("provider_type %q has no entry in the wire brand catalog", providerType)
			continue
		}
		assert.Equal(t, providerType, brand.ProviderType)
		assert.True(t, brand.API.Valid(), "brand %q declares wire_api %q", providerType, brand.API)
	}
}

// A provider created after migration 000098 states its provider_type but not its
// wire protocol. The declaration must come from the type's brand: defaulting to
// OpenAI-compatible would hand an anthropic/ollama/CLI/ChatGPT row to the wrong
// transport (the regression this pins).
func TestNormalizeProviderDeclarationDerivesFromBrand(t *testing.T) {
	for providerType := range ValidProviderTypes {
		p := &LLMProviderData{ProviderType: providerType}
		require.NoError(t, NormalizeProviderDeclaration(p), providerType)

		api, ok := wire.APIForBrand(providerType)
		require.True(t, ok, "provider_type %q has no brand", providerType)
		assert.Equal(t, string(api), p.WireAPI, providerType)
		assert.NotEmpty(t, p.AuthKind, providerType)
		assert.Equal(t, CurrentSettingsVersion, p.SettingsVersion, providerType)
	}
}

// The four legacy types whose transport is not OpenAI-compatible, as migration
// 000098 backfills them.
func TestNormalizeProviderDeclarationMatchesMigrationBackfill(t *testing.T) {
	for _, tc := range []struct {
		providerType string
		wireAPI      string
		authKind     string
	}{
		{ProviderAnthropicNative, WireAPIAnthropicMessages, AuthKindAPIKey},
		{ProviderChatGPTOAuth, WireAPIOpenAICodexResponses, AuthKindOAuthBrowser},
		{ProviderClaudeCLI, WireAPICLIDelegated, AuthKindCLIDelegated},
		{ProviderOllama, WireAPIOllamaNative, AuthKindNone},
		{ProviderOllamaCloud, WireAPIOllamaNative, AuthKindNone},
	} {
		p := &LLMProviderData{ProviderType: tc.providerType}
		require.NoError(t, NormalizeProviderDeclaration(p))
		assert.Equal(t, tc.wireAPI, p.WireAPI, tc.providerType)
		assert.Equal(t, tc.authKind, p.AuthKind, tc.providerType)
	}
}

func TestNormalizeProviderDeclarationRejectsUnknownType(t *testing.T) {
	p := &LLMProviderData{ProviderType: "not_a_provider"}
	err := NormalizeProviderDeclaration(p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not_a_provider")
	assert.Empty(t, p.WireAPI, "an unknown type must not silently become OpenAI-compatible")
}

func TestNormalizeProviderDeclarationKeepsStatedDeclaration(t *testing.T) {
	p := &LLMProviderData{
		ProviderType: ProviderOllama,
		WireAPI:      WireAPIOllamaNative,
		AuthKind:     AuthKindNone,
	}
	require.NoError(t, NormalizeProviderDeclaration(p))
	assert.Equal(t, WireAPIOllamaNative, p.WireAPI)
	assert.Equal(t, AuthKindNone, p.AuthKind)

	// An explicitly stated declaration also wins over the brand on a full row.
	p = &LLMProviderData{ProviderType: ProviderOpenAICompat, WireAPI: WireAPIOllamaNative, AuthKind: AuthKindNone}
	require.NoError(t, NormalizeProviderDeclaration(p))
	assert.Equal(t, WireAPIOllamaNative, p.WireAPI)
}

// Repointing a provider at another brand must repoint its declaration too, or the
// row keeps dispatching through the previous brand's transport.
func TestValidateProviderUpdatesDerivesDeclarationOnTypeChange(t *testing.T) {
	updates := map[string]any{"provider_type": ProviderAnthropicNative, "display_name": "x"}
	require.NoError(t, ValidateProviderUpdates(updates))
	assert.Equal(t, WireAPIAnthropicMessages, updates["wire_api"])
	assert.Equal(t, AuthKindAPIKey, updates["auth_kind"])

	stated := map[string]any{"provider_type": ProviderAnthropicNative, "wire_api": WireAPIOllamaNative, "auth_kind": AuthKindNone}
	require.NoError(t, ValidateProviderUpdates(stated))
	assert.Equal(t, WireAPIOllamaNative, stated["wire_api"])
	assert.Equal(t, AuthKindNone, stated["auth_kind"])

	// An unknown type is left alone rather than guessed; validation of the type
	// itself belongs to the surfaces that create providers.
	unknown := map[string]any{"provider_type": "not_a_provider"}
	require.NoError(t, ValidateProviderUpdates(unknown))
	assert.NotContains(t, unknown, "wire_api")
	assert.NotContains(t, unknown, "auth_kind")
}

// The migrations and the wire brand catalog must agree, in both directions: a row
// migrated from provider_type and a row created today with the same
// provider_type must end up on the same transport and credential source. This is
// what caught ollama_cloud being backfilled as OpenAI-compatible while its brand
// (and the old registry switch) used the native Ollama transport.
func TestMigrationBackfillAgreesWithWireBrands(t *testing.T) {
	const defaultWire, defaultAuth = "openai-completions", "api_key"
	wireCase, authCase := parseDeclarationCase(t, migrationPath(t))

	for providerType := range ValidProviderTypes {
		api, ok := wire.APIForBrand(providerType)
		require.True(t, ok, "provider_type %q has no brand", providerType)
		wantWire := wireCase[providerType]
		if wantWire == "" {
			wantWire = defaultWire
		}
		assert.Equal(t, string(api), wantWire, "provider_type %q: migration 000098 and the brand disagree on wire_api", providerType)

		d, ok := wire.Lookup(api)
		require.True(t, ok, "wire %q is not registered", api)
		wantAuth := authCase[providerType]
		if wantAuth == "" {
			wantAuth = defaultAuth
		}
		assert.Equal(t, string(d.AuthKind), wantAuth, "provider_type %q: migration 000098 and the brand disagree on auth_kind", providerType)
	}
}

func migrationPath(t *testing.T) string {
	t.Helper()
	for _, rel := range []string{"../../migrations/000098_provider_declaration.up.sql"} {
		if _, err := os.Stat(rel); err == nil {
			return rel
		}
	}
	t.Skip("migration file not present in this checkout")
	return ""
}

// parseDeclarationCase returns the provider_type → value maps of the two CASE
// blocks that backfill wire_api and auth_kind.
func parseDeclarationCase(t *testing.T, path string) (map[string]string, map[string]string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	body := string(raw)
	wireSection, authSection, hasAuth := strings.Cut(body, "SET auth_kind")
	require.True(t, hasAuth, "%s: no auth_kind backfill found", path)

	re := regexp.MustCompile(`(?m)^\s*WHEN\s+'([a-z_0-9]+)'\s+THEN\s+'([a-z_0-9-]+)'`)
	pairs := func(section string) map[string]string {
		out := map[string]string{}
		for _, m := range re.FindAllStringSubmatch(section, -1) {
			out[m[1]] = m[2]
		}
		return out
	}
	wireCase := pairs(wireSection)
	authCase := pairs(authSection)
	require.NotEmpty(t, wireCase, "%s: no wire_api CASE arms parsed", path)
	require.NotEmpty(t, authCase, "%s: no auth_kind CASE arms parsed", path)
	return wireCase, authCase
}

// The SQLite mirror carries the same backfill; a divergence between the two
// migration systems is a desktop-only bug.
func TestSQLiteBackfillMatchesPGMigration(t *testing.T) {
	wireCase, authCase := parseDeclarationCase(t, migrationPath(t))
	raw, err := os.ReadFile("sqlitestore/schema.go")
	require.NoError(t, err)
	sqliteWire, sqliteAuth := parseDeclarationCaseBody(t, string(raw))
	for providerType, want := range wireCase {
		assert.Equal(t, want, sqliteWire[providerType], "wire_api for %q differs between PG and SQLite", providerType)
	}
	for providerType, want := range authCase {
		assert.Equal(t, want, sqliteAuth[providerType], "auth_kind for %q differs between PG and SQLite", providerType)
	}
}

// parseDeclarationCaseBody parses CASE arms out of an in-source SQL string.
func parseDeclarationCaseBody(t *testing.T, body string) (map[string]string, map[string]string) {
	t.Helper()
	wireSection, authSection, hasAuth := strings.Cut(body, "SET auth_kind")
	require.True(t, hasAuth, "sqlite schema.go: no auth_kind backfill found")
	re := regexp.MustCompile(`WHEN '([a-z_0-9]+)'\s+THEN '([a-z_0-9-]+)'`)
	pairs := func(section string) map[string]string {
		out := map[string]string{}
		for _, m := range re.FindAllStringSubmatch(section, -1) {
			out[m[1]] = m[2]
		}
		return out
	}
	wireCase, authCase := pairs(wireSection), pairs(authSection)
	require.NotEmpty(t, wireCase, "sqlite schema.go: no wire_api CASE arms parsed")
	return wireCase, authCase
}
