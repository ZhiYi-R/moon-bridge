package app

import (
	"strings"

	"github.com/ZhiYi-R/moon-bridge/internal/db"
)

// ResolvePersistenceActiveProvider chooses the provider name that should be
// passed into db.Registry.Init. It preserves explicit configuration and only
// auto-selects when the startup environment makes the choice deterministic.
func ResolvePersistenceActiveProvider(configured string, providers []db.Provider) string {
	configured = strings.TrimSpace(configured)
	if configured != "" {
		return configured
	}
	if len(providers) == 1 {
		return providers[0].Name()
	}
	var workerBound db.Provider
	for _, provider := range providers {
		if provider == nil || !provider.Features().WorkerBound {
			continue
		}
		if workerBound != nil {
			return ""
		}
		workerBound = provider
	}
	if workerBound != nil {
		return workerBound.Name()
	}
	return ""
}
