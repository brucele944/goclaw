package providers

import (
	"errors"
	"strings"
	"sync"
)

// StrictScopeKey scopes strict-tool disabling to a specific (provider, base_url, model).
// A model rejecting `strict` mode must never disable strict tools for other models
// on the same provider or endpoint.
type StrictScopeKey struct {
	Provider string
	BaseURL  string
	Model    string
}

// StrictToolsRegistry manages in-memory strict tool opt-outs and triggers persistence.
type StrictToolsRegistry struct {
	mu       sync.RWMutex
	disabled map[StrictScopeKey]bool
	persist  func(key StrictScopeKey)
}

// NewStrictToolsRegistry creates a new registry with an optional persistence hook.
func NewStrictToolsRegistry(persist func(key StrictScopeKey)) *StrictToolsRegistry {
	return &StrictToolsRegistry{
		disabled: make(map[StrictScopeKey]bool),
		persist:  persist,
	}
}

var (
	defaultStrictRegistryMu sync.Mutex
	defaultStrictRegistry   = NewStrictToolsRegistry(nil)
)

// DefaultStrictTools returns the global in-memory strict tools registry.
func DefaultStrictTools() *StrictToolsRegistry {
	defaultStrictRegistryMu.Lock()
	defer defaultStrictRegistryMu.Unlock()
	return defaultStrictRegistry
}

// SetStrictToolsPersistHook sets the persistence callback for the global registry.
func SetStrictToolsPersistHook(hook func(key StrictScopeKey)) {
	defaultStrictRegistryMu.Lock()
	defer defaultStrictRegistryMu.Unlock()
	defaultStrictRegistry.persist = hook
}

// Disabled reports whether strict tool mode is disabled for the given scope.
func (r *StrictToolsRegistry) Disabled(key StrictScopeKey) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.disabled[key]
}

// Disable marks strict tool mode as disabled for the given scope and invokes the
// persistence hook.
func (r *StrictToolsRegistry) Disable(key StrictScopeKey) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.disabled[key] {
		r.mu.Unlock()
		return
	}
	r.disabled[key] = true
	persist := r.persist
	r.mu.Unlock()

	if persist != nil {
		persist(key)
	}
}

// Reset clears all in-memory entries (used for testing).
func (r *StrictToolsRegistry) Reset() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.disabled = make(map[StrictScopeKey]bool)
}

// IsStrictRejectionError checks if an error indicates the upstream model
// rejects OpenAI strict mode parameter schemas or the strict flag itself.
func IsStrictRejectionError(err error) bool {
	if err == nil {
		return false
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) && httpErr.Status != 400 && httpErr.Status != 422 {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "strict") && (strings.Contains(msg, "not supported") ||
		strings.Contains(msg, "unsupported") ||
		strings.Contains(msg, "invalid") ||
		strings.Contains(msg, "unknown") ||
		strings.Contains(msg, "schema")) {
		return true
	}
	if strings.Contains(msg, "additionalproperties") && strings.Contains(msg, "false") &&
		(strings.Contains(msg, "unsupported") || strings.Contains(msg, "not allowed")) {
		return true
	}
	return false
}
