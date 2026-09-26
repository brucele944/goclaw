package discovery

import (
	"context"
	"fmt"
)

// staticDiscovery serves the bundled snapshot as the discovery result.
//
// Providers whose upstream has no model-listing endpoint (subprocess transports,
// OAuth transports and the platforms that never exposed /models) are discovered
// from the snapshot GoClaw ships. Running the snapshot through the same merge
// path as a live listing keeps one code path for seeding, merging and cache
// bookkeeping — and keeps a static provider's rows and served list identical.
type staticDiscovery struct{}

func (d *staticDiscovery) Type() string { return TypeStatic }

func (d *staticDiscovery) List(_ context.Context, p ProviderRef) ([]ModelInfo, error) {
	models := Bundled(p.ProviderType)
	if len(models) == 0 {
		return nil, Failed(ClassUnsupported, fmt.Errorf("no bundled model catalog for provider type %q", p.ProviderType))
	}
	return models, nil
}
