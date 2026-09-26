// Package discovery asks a provider's upstream which models it currently
// serves.
//
// The catalogue of record lives in `llm_models` rows (internal/providers/catalog
// owns seeding, merging and the fingerprint cache); this package only answers
// "what does the upstream list right now?" and never touches the database.
//
// Failure contract: every implementation returns an error — never an empty
// slice — so a caller can tell "this upstream serves no models" apart from
// "listing is broken" and keep serving the last good rows with stale=true
// instead of silently withdrawing a working model list from the UI.
package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

// Discovery type names. They are also the accepted values of a provider row's
// settings.discovery override.
const (
	TypeOpenAIModelsList = "openai-models-list"
	TypeOllama           = "ollama"
	TypeProxy            = "proxy"
	TypeLiteLLM          = "litellm"
	TypeStatic           = "static"
)

// ModelInfo is one model as reported by an upstream listing endpoint. Unknown
// metadata stays nil so a caller can leave the matching column NULL instead of
// guessing (a zero context window is not a real value).
type ModelInfo struct {
	ID            string
	DisplayName   string
	ContextWindow *int
	MaxTokens     *int
	// Tokenizer is the tokencount tokenizer id when a source knows it
	// (llm_models.tokenizer).
	Tokenizer string
	// Capabilities are the llm_models.capabilities keys the upstream (or the
	// bundled snapshot) proves: tool_calling, vision, reasoning, …
	Capabilities map[string]bool
	// Modalities are the input modalities the model accepts (llm_models.modalities).
	Modalities []string
}

// ProviderRef is a provider row reduced to what an upstream listing needs. It
// carries no store types so the discovery implementations stay testable without
// a database.
type ProviderRef struct {
	ID           uuid.UUID
	Name         string
	ProviderType string
	// WireAPI is the declared llm_providers.wire_api; it selects the auth and
	// response shape of an otherwise identical /models endpoint.
	WireAPI string
	// BaseURL is the resolved api_base. Empty means "use the implementation's
	// vendor default".
	BaseURL string
	APIKey  string
	// ExtraHeaders are vendor-required identity headers (wire brand data).
	ExtraHeaders map[string]string
	Settings     json.RawMessage
}

// Discovery lists the models of one upstream protocol.
type Discovery interface {
	// Type returns the discovery type name (one of the Type* constants).
	Type() string
	// List returns the upstream's model list. It returns an error — never an
	// empty slice — when the upstream cannot be asked or answers unusably.
	List(ctx context.Context, p ProviderRef) ([]ModelInfo, error)
}

// URLGuard rejects outbound URLs that must not be reached. internal/http passes
// the same guard provider create/verify use (validateProviderURL) so discovery
// cannot become an SSRF hole for a row written by direct SQL.
type URLGuard func(rawURL, providerType string) error

// Registry resolves a provider declaration to its Discovery implementation.
type Registry struct {
	guard URLGuard
	impls map[string]Discovery
}

// NewRegistry builds the discovery registry. A nil guard disables the
// caller-supplied URL check; the scheme check inside the HTTP implementations
// still applies.
func NewRegistry(guard URLGuard) *Registry {
	r := &Registry{
		guard: guard,
		impls: make(map[string]Discovery, 5),
	}
	for _, d := range []Discovery{
		&openAIModelsList{guard: guard},
		&ollamaDiscovery{guard: guard},
		&proxyDiscovery{guard: guard},
		&liteLLMDiscovery{guard: guard},
		&staticDiscovery{},
	} {
		r.impls[d.Type()] = d
	}
	return r
}

// Register adds or replaces an implementation. The registry is built from the
// shipped set; this exists so an edition — or a test — can substitute one
// protocol without forking the constructor.
func (r *Registry) Register(d Discovery) {
	if d == nil || d.Type() == "" {
		return
	}
	r.impls[d.Type()] = d
}

// SettingsDiscoveryKey is the llm_providers.settings key that overrides the
// default discovery type of a provider (e.g. a LiteLLM proxy stored as an
// OpenAI-compatible row sets {"discovery":"litellm"}).
const SettingsDiscoveryKey = "discovery"

// ResolveType returns the discovery type for a provider declaration.
//
// Precedence: an explicit settings.discovery override, then the provider type's
// catalogued behaviour (the legacy hardcoded-catalog brands list static / native
// endpoints), then the declared wire protocol.
func ResolveType(providerType, wireAPI string, settings json.RawMessage) string {
	if explicit := settingsDiscoveryType(settings); explicit != "" {
		return explicit
	}
	switch providerType {
	case "ollama", "ollama_cloud":
		return TypeOllama
	case "claude_cli", "acp", "chatgpt_oauth":
		// Subprocess / OAuth transports have no listing endpoint: the bundled
		// snapshot is the catalogue (today's behaviour).
		return TypeStatic
	case "bailian", "dashscope", "minimax_native", "zai", "zai_coding", "aimlapi":
		// These platforms do not expose a /models endpoint.
		return TypeStatic
	case "openrouter":
		return TypeProxy
	case "litellm", "liteLLM":
		return TypeLiteLLM
	}
	switch wireAPI {
	case "ollama-native":
		return TypeOllama
	case "cli-delegated", "openai-codex-responses":
		return TypeStatic
	}
	return TypeOpenAIModelsList
}

// settingsDiscoveryType extracts a valid settings.discovery override.
func settingsDiscoveryType(settings json.RawMessage) string {
	if len(settings) == 0 {
		return ""
	}
	var s struct {
		Discovery string `json:"discovery"`
	}
	if err := json.Unmarshal(settings, &s); err != nil {
		return ""
	}
	v := strings.ToLower(strings.TrimSpace(s.Discovery))
	switch v {
	case TypeOpenAIModelsList, TypeOllama, TypeProxy, TypeLiteLLM, TypeStatic:
		return v
	default:
		return ""
	}
}

// For returns the implementation for a provider declaration.
func (r *Registry) For(providerType, wireAPI string, settings json.RawMessage) (Discovery, bool) {
	if r == nil {
		return nil, false
	}
	d, ok := r.impls[ResolveType(providerType, wireAPI, settings)]
	return d, ok
}

// Discover resolves and runs the provider's discovery implementation.
func (r *Registry) Discover(ctx context.Context, p ProviderRef) ([]ModelInfo, error) {
	d, ok := r.For(p.ProviderType, p.WireAPI, p.Settings)
	if !ok {
		err := fmt.Errorf("no discovery implementation for provider type %q (wire_api %q)", p.ProviderType, p.WireAPI)
		slog.Warn("providers.discovery.unresolved", "provider", p.Name, "provider_type", p.ProviderType, "wire_api", p.WireAPI)
		return nil, Failed(ClassUnsupported, err)
	}
	if p.ProviderType != "" {
		slog.Debug("providers.discovery", "provider", p.Name, "type", d.Type())
	}
	return d.List(ctx, p)
}

// Failure classes reported in the /v1/providers/{id}/models error_class field.
const (
	ClassUnsupported = "unsupported"
	ClassInvalidURL  = "invalid_url"
	ClassAuth        = "auth"
	ClassNotFound    = "not_found"
	ClassHTTP        = "http"
	ClassNetwork     = "network"
	ClassTimeout     = "timeout"
	ClassDecode      = "decode"
	ClassUnknown     = "unknown"
)

// Error is a classified discovery failure.
type Error struct {
	Class string
	Err   error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// Failed wraps err with a failure class.
func Failed(class string, err error) error {
	if err == nil {
		return nil
	}
	var existing *Error
	if errors.As(err, &existing) {
		if class == "" || class == ClassUnknown {
			return err
		}
	}
	return &Error{Class: class, Err: err}
}

// ClassOf returns the failure class of a discovery error (ClassUnknown when the
// error is not classified, "" when err is nil).
func ClassOf(err error) string {
	if err == nil {
		return ""
	}
	var de *Error
	if errors.As(err, &de) {
		return de.Class
	}
	return ClassUnknown
}

// httpStatusError is a non-2xx upstream answer.
type httpStatusError struct {
	Status int
	Body   string
	What   string
}

func (e *httpStatusError) Error() string {
	body := strings.TrimSpace(e.Body)
	if len(body) > 256 {
		body = body[:256]
	}
	if body == "" {
		return fmt.Sprintf("%s returned HTTP %d", e.What, e.Status)
	}
	return fmt.Sprintf("%s returned HTTP %d: %s", e.What, e.Status, body)
}

// decodeError is an unusable (unparseable) upstream answer.
type decodeError struct {
	What string
	Err  error
}

func (e *decodeError) Error() string { return fmt.Sprintf("decode %s response: %v", e.What, e.Err) }
func (e *decodeError) Unwrap() error { return e.Err }

// Classify maps an arbitrary outbound error to a failure class.
func Classify(err error) string {
	if err == nil {
		return ""
	}
	var de *Error
	if errors.As(err, &de) {
		return de.Class
	}
	var se *httpStatusError
	if errors.As(err, &se) {
		switch {
		case se.Status == http.StatusUnauthorized || se.Status == http.StatusForbidden || se.Status == http.StatusPaymentRequired:
			return ClassAuth
		case se.Status == http.StatusNotFound || se.Status == http.StatusMethodNotAllowed:
			return ClassNotFound
		default:
			return ClassHTTP
		}
	}
	var dErr *decodeError
	if errors.As(err, &dErr) {
		return ClassDecode
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ClassTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return ClassTimeout
		}
		return ClassNetwork
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return ClassNetwork
	}
	return ClassUnknown
}

// guardURL enforces the caller-supplied SSRF guard plus the scheme check that
// applies to every outbound discovery request.
func (r *Registry) guardURL(rawURL, providerType string) error {
	return checkURL(r.guard, rawURL, providerType)
}

// checkURL is the guard implementation shared by the registry and by
// implementations that hold a guard directly.
func checkURL(guard URLGuard, rawURL, providerType string) error {
	if rawURL == "" {
		return Failed(ClassInvalidURL, errors.New("empty provider base URL"))
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return Failed(ClassInvalidURL, fmt.Errorf("invalid provider URL: %w", err))
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return Failed(ClassInvalidURL, fmt.Errorf("provider URL must use http or https scheme, got %q", u.Scheme))
	}
	if guard != nil {
		if err := guard(rawURL, providerType); err != nil {
			return Failed(ClassInvalidURL, err)
		}
	}
	return nil
}

// trimBase normalizes a base URL for path appending.
func trimBase(base string) string { return strings.TrimRight(strings.TrimSpace(base), "/") }

// readLimited reads at most n bytes of a response body (error bodies are only
// needed for diagnostics).
func readLimited(r io.Reader, n int64) ([]byte, error) { return io.ReadAll(io.LimitReader(r, n)) }

// doJSON issues a GET/POST and decodes the JSON body into out, classifying
// every failure so callers can surface a stable error_class.
func doJSON(ctx context.Context, method, rawURL string, headers map[string]string, body []byte, what string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, bytes.NewReader(body))
	if err != nil {
		return Failed(ClassInvalidURL, err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Failed(Classify(err), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := readLimited(resp.Body, 1024)
		return Failed(Classify(&httpStatusError{Status: resp.StatusCode, Body: string(raw), What: what}),
			&httpStatusError{Status: resp.StatusCode, Body: string(raw), What: what})
	}
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(out); err != nil {
		return Failed(ClassDecode, &decodeError{What: what, Err: err})
	}
	return nil
}
