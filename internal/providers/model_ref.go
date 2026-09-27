package providers

import "strings"

// SplitModelRef resolves a per-request model reference against the caller's
// provider namespace.
//
// "<provider>/<model>" is split only when the prefix names a provider the caller
// knows about: a vendor model id that itself contains a slash (OpenRouter's
// "openai/gpt-5.5") must not be mistaken for a provider reference, and a bare
// model id keeps the caller's own provider — the pre-existing semantics of a
// per-request model override.
//
// providerKnown may be nil, in which case every reference is treated as a bare
// model id.
func SplitModelRef(ref string, providerKnown func(name string) bool) (providerName, modelID string, hasProvider bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", "", false
	}
	prefix, rest, found := strings.Cut(ref, "/")
	if !found || prefix == "" || rest == "" || providerKnown == nil || !providerKnown(prefix) {
		return "", ref, false
	}
	return prefix, rest, true
}
