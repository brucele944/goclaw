package wire

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/providers"
)

// TestAllMatchesDeclaredEnum pins the registry against the declared enum: every
// wire_api the store accepts must have a descriptor, and vice versa.
func TestAllMatchesDeclaredEnum(t *testing.T) {
	descriptors := All()
	if len(descriptors) != len(apiOrder) {
		t.Fatalf("All() returned %d descriptors, want %d", len(descriptors), len(apiOrder))
	}
	seen := make(map[API]bool, len(descriptors))
	for i, d := range descriptors {
		if seen[d.API] {
			t.Fatalf("All() returned %s twice", d.API)
		}
		seen[d.API] = true
		if want := apiOrder[i]; d.API != want {
			t.Fatalf("All()[%d] = %s, want %s (canonical order)", i, d.API, want)
		}
		if !d.API.Valid() {
			t.Fatalf("descriptor %s is not in the declared enum", d.API)
		}
		if !d.AuthKind.Valid() {
			t.Fatalf("descriptor %s has undeclared auth kind %q", d.API, d.AuthKind)
		}
		if d.Build == nil {
			t.Fatalf("descriptor %s has no Build", d.API)
		}
		if _, ok := Lookup(d.API); !ok {
			t.Fatalf("Lookup(%s) = false, want the registered descriptor", d.API)
		}
	}
	for _, api := range apiOrder {
		if !seen[api] {
			t.Fatalf("declared wire_api %s has no descriptor", api)
		}
	}
}

func TestLookupRejectsUnregisteredAPI(t *testing.T) {
	if _, ok := Lookup("not-a-wire-api"); ok {
		t.Fatal("Lookup of an unregistered wire_api should fail")
	}
	if _, ok := Lookup(OpenAIResponses); !ok {
		t.Fatal("Lookup should find a registered wire_api")
	}
}

// TestBuildUnknownAPIIsNeverOpenAI guards the "no silent default" contract: an
// unregistered wire_api is an error naming the provider, not a fallback.
func TestBuildUnknownAPIIsNeverOpenAI(t *testing.T) {
	prov, err := Build(Config{API: "mystery-wire", Name: "acme"})
	if err == nil {
		t.Fatalf("Build() = %T, want error for an unregistered wire_api", prov)
	}
	var unknown *UnknownAPIError
	if !asUnknownAPI(err, &unknown) {
		t.Fatalf("Build() error = %v, want *UnknownAPIError", err)
	}
	if unknown.Name != "acme" {
		t.Fatalf("error names provider %q, want %q", unknown.Name, "acme")
	}
	for _, want := range []string{"acme", "mystery-wire", string(OpenAICompletions)} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err.Error(), want)
		}
	}
}

func TestBrandDefaultsCoverShippedBrands(t *testing.T) {
	for _, brandType := range BrandProviderTypes() {
		base, model, _, err := BrandDefaults(brandType)
		if err != nil {
			t.Fatalf("BrandDefaults(%q) error = %v", brandType, err)
		}
		brand, ok := BrandFor(brandType)
		if !ok {
			t.Fatalf("BrandProviderTypes() listed %q but BrandFor cannot find it", brandType)
		}
		if base != brand.BaseURL || model != brand.Model {
			t.Fatalf("BrandDefaults(%q) = (%q, %q), want (%q, %q)", brandType, base, model, brand.BaseURL, brand.Model)
		}
		if !brand.API.Valid() {
			t.Fatalf("brand %q declares invalid wire_api %q", brandType, brand.API)
		}
	}
	if _, _, _, err := BrandDefaults("no-such-brand"); err == nil {
		t.Fatal("BrandDefaults(unknown) should error")
	}
}

// TestBrandExtraHeadersAreNotShared verifies the catalog cannot be mutated by a
// caller through the returned header map.
func TestBrandExtraHeadersAreNotShared(t *testing.T) {
	_, _, headers, err := BrandDefaults("kimi_coding")
	if err != nil {
		t.Fatal(err)
	}
	if headers["User-Agent"] == "" {
		t.Fatalf("kimi_coding headers = %v, want a User-Agent", headers)
	}
	headers["User-Agent"] = "mutated"
	_, _, again, _ := BrandDefaults("kimi_coding")
	if again["User-Agent"] == "mutated" {
		t.Fatal("BrandDefaults leaked a mutable reference to the catalog headers")
	}
}

// TestBuildOpenAICompletionsUsesDeclaredBaseAndBrandDefaults covers the two base
// URL paths: an explicit api_base wins, otherwise the brand default applies.
func TestBuildOpenAICompletionsUsesDeclaredBaseAndBrandDefaults(t *testing.T) {
	t.Run("explicit base wins", func(t *testing.T) {
		prov, err := Build(Config{
			API:          OpenAICompletions,
			Source:       SourceDB,
			Name:         "custom-gateway",
			ProviderType: "zai",
			APIKey:       "k",
			BaseURL:      "https://gateway.internal/v1",
		})
		if err != nil {
			t.Fatal(err)
		}
		openai, ok := prov.(*providers.OpenAIProvider)
		if !ok {
			t.Fatalf("prov = %T, want *providers.OpenAIProvider", prov)
		}
		if got := openai.APIBase(); got != "https://gateway.internal/v1" {
			t.Fatalf("APIBase() = %q, want the declared base", got)
		}
		if got := openai.DefaultModel(); got != "glm-5.2" {
			t.Fatalf("DefaultModel() = %q, want the brand default", got)
		}
	})

	t.Run("brand default base", func(t *testing.T) {
		prov, err := Build(Config{
			API:          OpenAICompletions,
			Source:       SourceDB,
			Name:         "my-zai",
			ProviderType: "zai",
			APIKey:       "k",
		})
		if err != nil {
			t.Fatal(err)
		}
		openai := prov.(*providers.OpenAIProvider)
		if got := openai.APIBase(); got != "https://api.z.ai/api/paas/v4" {
			t.Fatalf("APIBase() = %q, want the zai brand default", got)
		}
		// zai never reflected provider_type into the transport, not even on the DB
		// path; that rule is catalog data now.
		if got := openai.ProviderType(); got != "" {
			t.Fatalf("ProviderType() = %q, want no reflection for zai", got)
		}
	})

	t.Run("reflection follows the catalog per call site", func(t *testing.T) {
		dbProv, err := Build(Config{
			API: OpenAICompletions, Source: SourceDB,
			Name: "byteplus-db", ProviderType: "byteplus", APIKey: "k",
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := dbProv.(*providers.OpenAIProvider).ProviderType(); got != "byteplus" {
			t.Fatalf("DB byteplus ProviderType() = %q, want byteplus", got)
		}
		cfgProv, err := Build(Config{
			API: OpenAICompletions, Source: SourceConfig,
			Name: "byteplus", ProviderType: "byteplus", APIKey: "k",
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := cfgProv.(*providers.OpenAIProvider).ProviderType(); got != "byteplus" {
			t.Fatalf("config byteplus ProviderType() = %q, want byteplus", got)
		}
		// openai_compat is DB-only reflection: the config path leaves it unset.
		cfgCompat, err := Build(Config{
			API: OpenAICompletions, Source: SourceConfig,
			Name: "openai", ProviderType: "openai_compat", APIKey: "k",
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := cfgCompat.(*providers.OpenAIProvider).ProviderType(); got != "" {
			t.Fatalf("config openai_compat ProviderType() = %q, want no reflection", got)
		}
	})

	t.Run("unknown brand keeps the declared base and reflects type", func(t *testing.T) {
		prov, err := Build(Config{
			API:          OpenAICompletions,
			Source:       SourceDB,
			Name:         "bespoke",
			ProviderType: "bespoke_proxy",
			APIKey:       "k",
			BaseURL:      "https://bespoke.example/v1",
		})
		if err != nil {
			t.Fatal(err)
		}
		openai := prov.(*providers.OpenAIProvider)
		if got := openai.ProviderType(); got != "bespoke_proxy" {
			t.Fatalf("ProviderType() = %q, want the custom brand passed through", got)
		}
	})
}

// TestBuildAppliesRequestTimeout proves the declared timeout reaches the
// provider, which is what bounds a stalled upstream.
func TestBuildAppliesRequestTimeout(t *testing.T) {
	prov, err := Build(Config{
		API:          OpenAICompletions,
		Source:       SourceDB,
		Name:         "slow",
		ProviderType: "groq",
		APIKey:       "k",
		Timeout:      5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := prov.(*providers.OpenAIProvider).RequestTimeout(); got != 5*time.Second {
		t.Fatalf("RequestTimeout() = %v, want 5s", got)
	}
}

func TestTimeoutFromSettings(t *testing.T) {
	cases := []struct {
		name     string
		settings string
		want     time.Duration
	}{
		{"unset", ``, 0},
		{"absent", `{"num_ctx":4096}`, 0},
		{"seconds", `{"timeout_sec":5}`, 5 * time.Second},
		{"zero", `{"timeout_sec":0}`, 0},
		{"negative", `{"timeout_sec":-1}`, 0},
		{"malformed", `{`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TimeoutFromSettings(json.RawMessage(tc.settings)); got != tc.want {
				t.Fatalf("TimeoutFromSettings(%s) = %v, want %v", tc.settings, got, tc.want)
			}
		})
	}
}

// TestCLIDelegatedRequiresKnownBrand: sharing one wire API must not mean one
// transport; the brand decides.
func TestCLIDelegatedRequiresKnownBrand(t *testing.T) {
	_, err := Build(Config{API: CLIDelegated, Source: SourceDB, Name: "x", ProviderType: "mystery", CLI: &CLISettings{Path: "claude"}})
	if err == nil {
		t.Fatal("Build() should reject a cli-delegated row with no known subprocess brand")
	}
	if _, err := Build(Config{API: CLIDelegated, Source: SourceDB, Name: "x", ProviderType: "acp", CLI: &CLISettings{Path: "echo", WorkDir: t.TempDir()}}); err != nil {
		t.Fatalf("Build() for an acp brand error = %v", err)
	}
}

func TestCodexRequiresTokenSource(t *testing.T) {
	if _, err := Build(Config{API: OpenAICodexResponses, Source: SourceDB, Name: "chatgpt"}); err == nil {
		t.Fatal("Build() should require a token source for openai-codex-responses")
	}
}

func TestOpenAIResponsesHasNoTransport(t *testing.T) {
	if _, err := Build(Config{API: OpenAIResponses, Source: SourceDB, Name: "responses"}); err == nil {
		t.Fatal("Build() should report that openai-responses has no transport")
	}
}

// TestThinkingSettingsAreBrandGated: settings.thinking_enabled reached the
// transport only for brands built by the generic branch before this phase; the
// dedicated branches ignored it, and a brand catalog decides which is which.
func TestThinkingSettingsAreBrandGated(t *testing.T) {
	on := true
	generic, err := Build(Config{
		API: OpenAICompletions, Source: SourceDB,
		Name: "my-openrouter", ProviderType: "openrouter", APIKey: "k", Thinking: &on,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := generic.(*providers.OpenAIProvider).ThinkingEnabled(); got == nil || !*got {
		t.Fatalf("openrouter ThinkingEnabled() = %v, want true from settings", got)
	}

	dedicated, err := Build(Config{
		API: OpenAICompletions, Source: SourceDB,
		Name: "my-zai", ProviderType: "zai", APIKey: "k", Thinking: &on,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := dedicated.(*providers.OpenAIProvider).ThinkingEnabled(); got != nil {
		t.Fatalf("zai ThinkingEnabled() = %v, want nil (its dedicated branch ignored settings)", got)
	}

	// Ollama kept honouring the override on the DB path.
	ollamaProv, err := Build(Config{
		API: OllamaNative, Source: SourceDB,
		Name: "my-ollama", ProviderType: "ollama", Thinking: &on,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := ollamaProv.(*providers.OllamaProvider).ThinkingEnabled(); got == nil || !*got {
		t.Fatalf("ollama ThinkingEnabled() = %v, want true from settings", got)
	}
}

func asUnknownAPI(err error, target **UnknownAPIError) bool {
	return errors.As(err, target)
}
