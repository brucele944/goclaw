package store

import (
	"sort"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/providers/wire"
)

// The HTTP create path validates provider_type against ValidProviderTypes while
// the wire layer resolves the vendor defaults (base URL, default model) from the
// brand catalog. A brand added to only one of the two lists is either rejected on
// create ("unsupported provider_type") or accepted without defaults — adding the
// OpenCode brands initially hit exactly that (2026-09-28).
func TestValidProviderTypesMatchesBrandCatalog(t *testing.T) {
	brands := map[string]bool{}
	for _, b := range wire.BrandProviderTypes() {
		brands[b] = true
	}
	// "openai" is a catalogued brand without a creatable type of its own: the
	// creatable variant is openai_compat.
	delete(brands, "openai")

	var rejected, defaultless []string
	for b := range brands {
		if !ValidProviderTypes[b] {
			rejected = append(rejected, b)
		}
	}
	for k := range ValidProviderTypes {
		if !brands[k] {
			defaultless = append(defaultless, k)
		}
	}

	sort.Strings(rejected)
	sort.Strings(defaultless)
	if len(rejected) > 0 {
		t.Errorf("brands missing from ValidProviderTypes (create would fail): %v", rejected)
	}
	if len(defaultless) > 0 {
		t.Errorf("provider types without a brand entry (no vendor defaults): %v", defaultless)
	}
}
