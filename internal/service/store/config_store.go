// Package store implements the ConfigStore persistence layer for Moon Bridge.
// It provides a db.Consumer-backed interface for CRUD operations on configuration
// data, with staging validation and atomic apply.
package store

import (
	"context"

	"moonbridge/internal/config"
)

// ReloadFunc is called by ApplyPendingChanges to apply a new configuration
// after successful staging validation.
type ReloadFunc func(cfg *config.Config) error

// ConfigStore is the interface for configuration persistence operations.
type ConfigStore interface {
	// StageChange writes a pending change to the changes table.
	// Returns the change ID.
	StageChange(ChangeRow) (int64, error)

	// ListPendingChanges returns all changes that have not been applied.
	ListPendingChanges() ([]ChangeRow, error)

	// ApplyPendingChanges applies all pending changes in a transaction,
	// performs staging validation via the ReloadFunc, and marks changes as applied.
	ApplyPendingChanges(ctx context.Context, applier ReloadFunc) error

	// DiscardPendingChanges removes all pending changes.
	DiscardPendingChanges() error

	// LoadAll reads the complete configuration from the main tables.
	LoadAll() (*config.Config, error)

	// SeedFromConfig populates the main tables from a config.Config.
	// Intended for first-time DB initialization.
	SeedFromConfig(cfg *config.Config) error

	// ExportYAML serializes the current DB state as YAML bytes.
	// If includeSecrets is false, API key values are masked.
	ExportYAML(includeSecrets bool) ([]byte, error)
}

// ProviderRow represents a row in the config_store_providers table.
type ProviderRow struct {
	Key        string
	BaseURL    string
	APIKey     string
	Version    string
	Protocol   string
	Enabled    bool
	UserAgent  string
	WebSearch  string // JSON-serialized config.WebSearchFileConfig
	Extensions string // JSON-serialized map[string]config.ExtensionFileConfig
	CreatedAt  string
	UpdatedAt  string
}

// OfferRow represents a row in the config_store_offers table.
type OfferRow struct {
	ID           int64
	ProviderKey  string
	ModelSlug    string
	UpstreamName string
	Priority     int
	InputPrice   float64
	OutputPrice  float64
	CacheWrite   float64
	CacheRead    float64
	Overrides    string // JSON-serialized *config.ModelDefFileConfig
}

// ModelRow represents a row in the config_store_models table.
type ModelRow struct {
	Slug      string
	Metadata  string // JSON-serialized config.ModelDefFileConfig
	CreatedAt string
	UpdatedAt string
}

// RouteRow represents a row in the config_store_routes table.
type RouteRow struct {
	Alias           string
	ModelSlug       string
	ProviderKey     string
	DisplayName     string
	ContextWindow   int
	MaxOutputTokens int
	Extensions      string // JSON-serialized map[string]config.ExtensionFileConfig
	WebSearch       string // JSON-serialized config.WebSearchFileConfig
	CreatedAt       string
	UpdatedAt       string
}

// SettingRow represents a row in the config_store_settings table.
type SettingRow struct {
	Key   string
	Value string // JSON-serialized value
}

// ChangeRow represents a row in the config_store_changes table.
type ChangeRow struct {
	ID        int64
	BatchID   string
	Action    string // "create", "update", "delete"
	Resource  string // "provider", "offer", "model", "route", "setting"
	TargetKey string
	Before    string // JSON-serialized "before" state (empty for create)
	After     string // JSON-serialized "after" state (empty for delete)
	Applied   bool
	Error     string
	Revision  int
	CreatedAt string
	AppliedAt string
}
