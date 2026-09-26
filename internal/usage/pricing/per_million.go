package pricing

import (
	"context"
	"math/big"
	"strings"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// PerMillion is a price set in the unit the model catalogue and config.ModelPricing
// use: USD per 1M tokens. The pricing catalog itself stores USD per token (the
// OpenRouter convention), so every read of it has to convert once — here.
type PerMillion struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
}

// PerMillionFromFields converts a resolved pricing row into per-1M rates.
// The second result is false when the row carries no token price at all (a
// request-only or image-only row is not a token cost).
func PerMillionFromFields(fields store.UsagePricingFields) (PerMillion, bool) {
	if fields.Input == nil && fields.Output == nil {
		return PerMillion{}, false
	}
	return PerMillion{
		Input:      perTokenToPerMillion(fields.Input),
		Output:     perTokenToPerMillion(fields.Output),
		CacheRead:  perTokenToPerMillion(fields.CacheRead),
		CacheWrite: perTokenToPerMillion(fields.CacheWrite),
	}, true
}

// perTokenToPerMillion converts a USD-per-token decimal string to USD per 1M.
func perTokenToPerMillion(price *string) float64 {
	if price == nil {
		return 0
	}
	r, ok := new(big.Rat).SetString(strings.TrimSpace(*price))
	if !ok {
		return 0
	}
	r.Mul(r, big.NewRat(1_000_000, 1))
	f, _ := r.Float64()
	return f
}

// CatalogCostResolver returns a resolver that fills a model's per-1M cost from
// the usage pricing catalog — the same OpenRouter-synced source tracing and
// usage caps price calls with, so a model's cost never has two sources of truth.
//
// The lookup is catalog-only (no tenant override), which is what a
// provider-name-keyed model registry can express. A nil store (lite edition,
// where usage caps are PostgreSQL-only) yields nil so callers can skip
// installing the resolver.
func CatalogCostResolver(s store.UsageCapStore) func(provider, modelID string) *providers.ModelCost {
	if s == nil {
		return nil
	}
	return func(provider, modelID string) *providers.ModelCost {
		resolved, err := s.ResolvePricing(context.Background(), uuid.Nil, uuid.Nil, provider, "", modelID)
		if err != nil || resolved == nil {
			return nil
		}
		rates, ok := PerMillionFromFields(resolved.Pricing)
		if !ok {
			return nil
		}
		return &providers.ModelCost{
			InputPer1M:     rates.Input,
			OutputPer1M:    rates.Output,
			CacheReadPer1M: rates.CacheRead,
		}
	}
}
