package wire

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/providers/compat"
)

// Source is the registration call site. It matters because provider_type
// reflection into the transport is not uniform across the two call sites: the
// config-file path and the DB path disagree for several brands (see Brand.Reflect).
type Source int

const (
	// SourceConfig is cmd.registerProviders (config file / env).
	SourceConfig Source = iota
	// SourceDB is cmd.registerProvidersFromDB (llm_providers rows).
	SourceDB
)

// Config is everything a transport needs to be constructed from a declaration.
// It carries no brand branching: vendor defaults come from the brand catalog and
// anything brand-specific that the catalog cannot express (token sources, parsed
// settings, subprocess configuration) is assembled by the caller from stores the
// registry must not depend on.
type Config struct {
	// API is the declared wire protocol (llm_providers.wire_api).
	API API
	// Source is the registration call site, which decides provider_type reflection.
	Source Source
	// Name is the registry name (tenant-scoped at the call site).
	Name string
	// ProviderType is the legacy brand id (llm_providers.provider_type).
	ProviderType string
	// APIKey is the static credential. Empty for keyless and delegated auth.
	APIKey string
	// BaseURL overrides the brand's default api_base. Empty = use the brand default.
	BaseURL string
	// DefaultModel overrides the brand's default model. Empty = use the brand default.
	DefaultModel string
	// Timeout, when > 0, bounds a whole Chat/ChatStream call for this provider.
	// 0 keeps the transport default (no per-call deadline).
	Timeout time.Duration

	// ConcurrencyGate, when non-nil, bounds concurrent Chat/ChatStream calls
	// for this provider (llm_providers.settings.max_in_flight). nil keeps the
	// previous behavior: unbounded concurrency.
	ConcurrencyGate providers.ConcurrencyGate

	// Registry is the model registry used for forward-compat resolution.
	Registry providers.ModelRegistry
	// TokenSource supplies (and refreshes) the access token for OAuth wire APIs.
	TokenSource providers.TokenSource
	// Routing carries Codex pool defaults for the ChatGPT OAuth flow.
	Routing *providers.CodexRoutingDefaults
	// Vertex carries the service-account declaration for google-vertex rows.
	Vertex *VertexSettings
	// CLI carries the subprocess configuration for cli-delegated rows.
	CLI *CLISettings
	// Thinking is the provider-level thinking override from settings.
	Thinking *bool
	// OllamaNumCtx is the explicit options.num_ctx override from settings.
	OllamaNumCtx *int

	// EndpointFamily is the declared endpoint family. Empty = infer from the
	// provider_type and base URL (never from the provider name).
	EndpointFamily string
	// Quirks are the operator's provider_quirks rows for this wire API. The
	// bundled seeds always apply; these override or suppress them.
	Quirks []compat.Quirk
	// ModelCompat is the per-model llm_models.compat fragment by model id,
	// resolved once here so the request path only swaps pointers.
	ModelCompat map[string]json.RawMessage
}

// VertexSettings is the project_id/region/model part of a google-vertex row's
// settings JSONB (the credential itself travels in Config.APIKey). The config
// file additionally allows a credentials file path.
type VertexSettings struct {
	ProjectID string `json:"project_id"`
	Region    string `json:"region"`
	Model     string `json:"model,omitempty"`
	// CredentialsFile is the config-file-only alternative to inline credentials.
	CredentialsFile string `json:"credentials_file,omitempty"`
}

// CLISettings is the assembled subprocess configuration for wire API
// cli-delegated. Which subprocess contract applies is the brand's CLIKind, so
// the registry — not the caller — owns the claude_cli vs acp decision.
type CLISettings struct {
	Name    string
	Path    string
	Model   string
	Args    []string
	WorkDir string
	IdleTTL time.Duration
	// PermMode is the Claude CLI / ACP permission mode.
	PermMode string
	// DenyPatterns are shell deny patterns passed to the tool bridge.
	DenyPatterns []*regexp.Regexp
	// SecurityHooks installs the Claude CLI workspace/hook guards.
	SecurityHooks bool
	// MCP is the assembled MCP bridge configuration (may be nil).
	MCP *providers.MCPConfigData
}

// Descriptor is one wire protocol's dispatch entry: its declared metadata plus
// the constructor for its transport.
type Descriptor struct {
	API      API
	AuthKind AuthKind
	// RequiresAPIKey says whether an empty credential blocks registration. It is
	// false for keyless (local Ollama), service-account (Vertex) and delegated
	// (claude_cli/acp) rows, and true for static keys — including ChatGPT OAuth,
	// whose row must still carry a placeholder credential.
	RequiresAPIKey bool
	// DefaultBaseURL/DefaultModel are the wire family's own defaults, used when a
	// brand declares none. Brand-level values take precedence.
	DefaultBaseURL string
	DefaultModel   string
	// ChatPath is the request path of the primary chat operation.
	ChatPath string
	// EnvKeys is the ordered credential environment fallback for this wire family.
	EnvKeys []string
	// AuthHeaderStyle names how the credential is carried ("bearer", "x-api-key",
	// "oauth-bearer", "gcp-oauth2", "none").
	AuthHeaderStyle string
	// SupportsTools / SupportsStream / SupportsStreamWithTools declare capability
	// shape; dashscope's "no stream with tools" is the notable false.
	SupportsTools           bool
	SupportsStream          bool
	SupportsStreamWithTools bool
	// TokenizerID is the vendored tokenizer mapping for this wire family.
	TokenizerID string
	// Build constructs the transport.
	Build func(Config) (providers.Provider, error)
}

var registry = map[API]Descriptor{}

// Register adds (or replaces) a descriptor. It is meant to be called from
// package init only; the gateway never registers wire APIs at runtime.
func Register(d Descriptor) {
	if d.API == "" {
		panic("wire: Register called with an empty API")
	}
	registry[d.API] = d
}

// Lookup returns the descriptor for a declared wire protocol. The second result
// is false when nothing is registered for it — the caller must log and skip,
// never fall back to a default transport.
func Lookup(api API) (Descriptor, bool) {
	d, ok := registry[api]
	return d, ok
}

// All returns every descriptor in canonical (apiOrder) order.
func All() []Descriptor {
	out := make([]Descriptor, 0, len(registry))
	for _, api := range apiOrder {
		if d, ok := registry[api]; ok {
			out = append(out, d)
		}
	}
	return out
}

// UnknownAPIError is returned when a row declares a wire_api with no registered
// transport. It names the provider so the operator can act on it.
type UnknownAPIError struct {
	API  API
	Name string
}

func (e *UnknownAPIError) Error() string {
	name := e.Name
	if name == "" {
		name = "<unnamed>"
	}
	return fmt.Sprintf("provider %q declares wire_api %q, which this build cannot dispatch (valid: %s)",
		name, string(e.API), strings.Join(validAPIStrings(), ", "))
}

// Build constructs a transport for a declaration. An unregistered wire_api is an
// error, never a silent OpenAI-compatible fallback.
func Build(cfg Config) (providers.Provider, error) {
	d, ok := Lookup(cfg.API)
	if !ok {
		return nil, &UnknownAPIError{API: cfg.API, Name: cfg.Name}
	}
	if d.Build == nil {
		return nil, fmt.Errorf("wire: %s has no transport in this build", cfg.API)
	}
	prov, err := d.Build(cfg)
	if err != nil {
		return nil, fmt.Errorf("wire: provider %q (%s): %w", cfg.Name, cfg.API, err)
	}
	return prov, nil
}

// validAPIStrings returns the registered API names in deterministic order.
func validAPIStrings() []string {
	out := make([]string, 0, len(registry))
	for api := range registry {
		out = append(out, string(api))
	}
	sort.Strings(out)
	return out
}

// DecodeVertexSettings parses the project_id/region/model keys of a
// google-vertex row's settings. It returns nil when either required key is
// missing, matching store.ParseVertexProviderSettings.
func DecodeVertexSettings(settings json.RawMessage) *VertexSettings {
	if len(settings) == 0 {
		return nil
	}
	var s VertexSettings
	if err := json.Unmarshal(settings, &s); err != nil {
		return nil
	}
	if s.ProjectID == "" || s.Region == "" {
		return nil
	}
	return &s
}
