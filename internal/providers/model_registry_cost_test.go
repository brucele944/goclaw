package providers

import "testing"

// TestRegistryCostResolverFillsZeroCostSpecs proves the registry consults the
// pricing catalog for a seeded model whose Cost is structurally zero (the
// ModelRegistry seeds carry no price), which is what stops ModelSpec.Cost from
// being always-zero in production.
func TestRegistryCostResolverFillsZeroCostSpecs(t *testing.T) {
	reg := NewInMemoryRegistry()
	if spec := reg.Resolve("anthropic", "claude-opus-4-6"); spec == nil || !spec.Cost.IsZero() {
		t.Fatalf("precondition failed: %+v", spec)
	}

	calls := 0
	reg.SetCostResolver(func(provider, modelID string) *ModelCost {
		calls++
		if provider != "anthropic" || modelID != "claude-opus-4-6" {
			t.Errorf("resolver called with %s/%s", provider, modelID)
		}
		return &ModelCost{InputPer1M: 15, OutputPer1M: 75}
	})

	spec := reg.Resolve("anthropic", "claude-opus-4-6")
	if spec == nil {
		t.Fatal("spec = nil")
	}
	if spec.Cost.InputPer1M != 15 || spec.Cost.OutputPer1M != 75 {
		t.Fatalf("cost = %+v, want the resolved price", spec.Cost)
	}
	// The enriched spec is cached: one catalog lookup per model, not per call.
	_ = reg.Resolve("anthropic", "claude-opus-4-6")
	if calls != 1 {
		t.Fatalf("resolver calls = %d, want 1", calls)
	}
}

func TestRegistryCostResolverSkipsKnownCostsAndUnknownModels(t *testing.T) {
	reg := NewInMemoryRegistry()
	calls := 0
	reg.SetCostResolver(func(provider, modelID string) *ModelCost {
		calls++
		if modelID == "unknown-model" {
			return nil
		}
		return &ModelCost{InputPer1M: 1}
	})
	// A spec that already knows its cost is never re-priced.
	reg.Register(ModelSpec{ID: "priced", Provider: "openai", Cost: ModelCost{InputPer1M: 7}})
	if got := reg.Resolve("openai", "priced"); got.Cost.InputPer1M != 7 {
		t.Fatalf("cost = %+v, want the registered price", got.Cost)
	}
	if calls != 0 {
		t.Fatalf("resolver calls = %d, want 0 for a priced spec", calls)
	}

	// A catalogue miss is memoized so the store is not queried per run.
	reg.Register(ModelSpec{ID: "unknown-model", Provider: "openai"})
	if got := reg.Resolve("openai", "unknown-model"); !got.Cost.IsZero() {
		t.Fatalf("cost = %+v, want zero for an unpriced model", got.Cost)
	}
	_ = reg.Resolve("openai", "unknown-model")
	if calls != 1 {
		t.Fatalf("resolver calls = %d, want 1 (the miss is memoized)", calls)
	}
}

func TestRegistryIgnoresNilCostResolver(t *testing.T) {
	reg := NewInMemoryRegistry()
	reg.SetCostResolver(nil)
	if got := reg.Resolve("openai", "gpt-5.5"); got == nil || !got.Cost.IsZero() {
		t.Fatalf("spec = %+v, want the registered zero-cost spec", got)
	}
}
