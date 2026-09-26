package pricing

import (
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// TestPerMillionFromFields pins the unit conversion between the catalog (USD per
// token, the OpenRouter convention) and the model catalogue / ModelPricing unit
// (USD per 1M tokens). A wrong factor here would misprice every model by 1e6.
func TestPerMillionFromFields(t *testing.T) {
	in, out := "0.000003", "0.000015"
	cacheRead, cacheWrite := "0.0000003", "0.00000375"
	rates, ok := PerMillionFromFields(store.UsagePricingFields{
		Input:      &in,
		Output:     &out,
		CacheRead:  &cacheRead,
		CacheWrite: &cacheWrite,
	})
	if !ok {
		t.Fatal("ok = false, want a token price")
	}
	if rates.Input != 3 {
		t.Errorf("input = %v, want 3", rates.Input)
	}
	if rates.Output != 15 {
		t.Errorf("output = %v, want 15", rates.Output)
	}
	if rates.CacheRead != 0.3 {
		t.Errorf("cache read = %v, want 0.3", rates.CacheRead)
	}
	if rates.CacheWrite != 3.75 {
		t.Errorf("cache write = %v, want 3.75", rates.CacheWrite)
	}
}

func TestPerMillionFromFieldsEdgeCases(t *testing.T) {
	if _, ok := PerMillionFromFields(store.UsagePricingFields{}); ok {
		t.Error("an empty pricing row is not a token price")
	}
	request := "0.001"
	if _, ok := PerMillionFromFields(store.UsagePricingFields{Request: &request}); ok {
		t.Error("a request-only price is not a token price")
	}
	only := "0.000001"
	rates, ok := PerMillionFromFields(store.UsagePricingFields{Input: &only})
	if !ok || rates.Input != 1 || rates.Output != 0 {
		t.Fatalf("rates = %+v ok=%v", rates, ok)
	}
	bad := "not-a-number"
	if rates, ok := PerMillionFromFields(store.UsagePricingFields{Input: &bad}); !ok || rates.Input != 0 {
		t.Fatalf("malformed price must degrade to 0: %+v ok=%v", rates, ok)
	}
}

func TestCatalogCostResolverWithoutStore(t *testing.T) {
	if got := CatalogCostResolver(nil); got != nil {
		t.Fatalf("resolver = %v, want nil when there is no pricing catalog (lite edition)", got != nil)
	}
}
