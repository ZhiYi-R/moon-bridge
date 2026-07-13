// Package runtime provides a snapshot-based runtime that holds the active
// configuration, provider manager, and pricing data. The snapshot is
// updated atomically via an atomic.Pointer, allowing safe concurrent reads
// without locking.
package runtime

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/ZhiYi-R/moon-bridge/internal/config"
	"github.com/ZhiYi-R/moon-bridge/internal/service/provider"
	"github.com/ZhiYi-R/moon-bridge/internal/service/stats"
)

// ConfigSnapshot is an immutable snapshot of the runtime state.
type ConfigSnapshot struct {
	// Config is the resolved runtime configuration.
	Config config.Config

	// ProviderMgr is the fully initialized provider manager.
	ProviderMgr *provider.ProviderManager

	// Pricing maps model identifiers to their pricing details.
	Pricing map[string]stats.ModelPricing
}

// Runtime holds the active ConfigSnapshot and provides atomic access
// and reload capability.
type Runtime struct {
	snapshot atomic.Pointer[ConfigSnapshot]
	mu       sync.Mutex // guards Reload; not needed for Current()
}

// NewRuntime creates a Runtime with the given initial configuration.
func NewRuntime(cfg config.Config, providerMgr *provider.ProviderManager, pricing map[string]stats.ModelPricing) *Runtime {
	rt := &Runtime{}
	snapshot := &ConfigSnapshot{
		Config:      cfg,
		ProviderMgr: providerMgr,
		Pricing:     pricing,
	}
	rt.snapshot.Store(snapshot)
	return rt
}

// Current returns the current ConfigSnapshot. The returned pointer is safe
// to use and will not be mutated by future Reload calls.
func (rt *Runtime) Current() *ConfigSnapshot {
	return rt.snapshot.Load()
}

func (rt *Runtime) ValidateCandidate(cfg config.Config) error {
	if _, err := buildSnapshot(cfg, "runtime candidate"); err != nil {
		return err
	}
	return nil
}

// Reload validates the given config, builds a new ProviderManager, computes
// pricing, and atomically replaces the snapshot. Returns an error if
// validation or ProviderManager construction fails; the existing snapshot
// remains unchanged.
func (rt *Runtime) Reload(cfg config.Config) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	snapshot, err := buildSnapshot(cfg, "runtime reload")
	if err != nil {
		return err
	}
	rt.snapshot.Store(snapshot)
	return nil
}

func buildSnapshot(cfg config.Config, errorPrefix string) (*ConfigSnapshot, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("%s: config validation: %w", errorPrefix, err)
	}

	providerCfg := config.ProviderFromGlobalConfig(&cfg)
	providerDefs := provider.BuildProviderDefsFromConfig(providerCfg)
	modelRoutes := provider.BuildModelRoutesFromConfig(providerCfg)

	providerMgr, err := provider.NewProviderManager(providerDefs, modelRoutes)
	if err != nil {
		return nil, fmt.Errorf("%s: provider manager: %w", errorPrefix, err)
	}

	pricing := provider.BuildPricingFromConfig(providerCfg)

	return &ConfigSnapshot{
		Config:      cfg,
		ProviderMgr: providerMgr,
		Pricing:     pricing,
	}, nil
}
