// Package codex concentrates Codex CLI client compatibility logic,
// including model catalog DTOs, config generation, toolcall codec,
// and custom grammar helpers.
//
// This package lives in internal/extensions so it can be shared by
// internal/server, internal/bridge, and cmd/moonbridge without
// creating circular dependencies. It depends only on lower-level
// packages (internal/openai, internal/anthropic, internal/config).
package codex

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"moonbridge/internal/foundation/config"
)

// ModelInfo represents a model entry in the OpenAI /v1/models response.
type ModelInfo struct {
	Slug                        string                    `json:"slug"`
	DisplayName                 string                    `json:"display_name"`
	Description                 string                    `json:"description,omitempty"`
	DefaultReasoningLevel       string                    `json:"default_reasoning_level,omitempty"`
	SupportedReasoningLevels    []ReasoningLevelPresetDTO `json:"supported_reasoning_levels"`
	ShellType                   string                    `json:"shell_type"`
	Visibility                  string                    `json:"visibility"`
	SupportedInAPI              bool                      `json:"supported_in_api"`
	Priority                    int                       `json:"priority"`
	AdditionalSpeedTiers        []string                  `json:"additional_speed_tiers"`
	AvailabilityNux             *ModelAvailabilityNux     `json:"availability_nux"`
	Upgrade                     *ModelInfoUpgrade         `json:"upgrade"`
	BaseInstructions            string                    `json:"base_instructions"`
	SupportsReasoningSummaries  bool                      `json:"supports_reasoning_summaries"`
	DefaultReasoningSummary     string                    `json:"default_reasoning_summary"`
	SupportVerbosity            bool                      `json:"support_verbosity"`
	DefaultVerbosity            *string                   `json:"default_verbosity"`
	ApplyPatchToolType          *string                   `json:"apply_patch_tool_type"`
	WebSearchToolType           string                    `json:"web_search_tool_type"`
	TruncationPolicy            TruncationPolicyConfig    `json:"truncation_policy"`
	SupportsParallelToolCalls   bool                      `json:"supports_parallel_tool_calls"`
	SupportsImageDetailOriginal bool                      `json:"supports_image_detail_original"`
	ContextWindow               *int                      `json:"context_window,omitempty"`
	MaxContextWindow            *int                      `json:"max_context_window,omitempty"`
	AutoCompactTokenLimit       *int                      `json:"auto_compact_token_limit,omitempty"`
	EffectiveContextWindowPct   int                       `json:"effective_context_window_percent"`
	ExperimentalSupportedTools  []string                  `json:"experimental_supported_tools"`
	InputModalities             []string                  `json:"input_modalities"`
	SupportsSearchTool          bool                      `json:"supports_search_tool"`
}

// ModelAvailabilityNux is a placeholder for Codex model availability nux.
type ModelAvailabilityNux struct{}

// ModelInfoUpgrade is a placeholder for Codex model upgrade info.
type ModelInfoUpgrade struct{}

// TruncationPolicyConfig matches Codex's truncation_policy field.
type TruncationPolicyConfig struct {
	Mode  string `json:"mode"`
	Limit int64  `json:"limit"`
}

const (
	defaultApplyPatchToolType = "freeform"
	// DefaultCatalogTruncationLimit keeps shell tool output from being clamped
	// to zero while using a consistent token policy across generated models.
	DefaultCatalogTruncationLimit int64 = 10000
)

// ReasoningLevelPresetDTO is the JSON shape Codex expects for reasoning presets.
type ReasoningLevelPresetDTO struct {
	Effort      string `json:"effort"`
	Description string `json:"description"`
}

// BuildModelInfoFromRoute creates a Codex-compatible ModelInfo from a route entry.
func BuildModelInfoFromRoute(alias string, ownedBy string, route config.RouteEntry) ModelInfo {
	displayName := route.DisplayName
	if displayName == "" {
		displayName = alias
	}
	displayName = displayName + "(" + ownedBy + ")"
	return newModelInfo(alias, displayName, route.Description, route.ContextWindow,
		route.DefaultReasoningLevel, route.SupportedReasoningLevels,
		route.SupportsReasoningSummaries, route.DefaultReasoningSummary,
		route.BaseInstructions)
}

// BuildModelInfoFromProviderModel creates a Codex-compatible ModelInfo from a
// provider model catalog entry.
func BuildModelInfoFromProviderModel(slug string, ownedBy string, meta config.ModelMeta) ModelInfo {
	displayName := meta.DisplayName
	if displayName == "" {
		// Extract model name from "model(provider)" slug format.
		if idx := strings.Index(slug, "("); idx > 0 {
			displayName = slug[:idx]
		} else {
			displayName = slug
		}
	}
	displayName = displayName + "(" + ownedBy + ")"
	return newModelInfo(slug, displayName, meta.Description, meta.ContextWindow,
		meta.DefaultReasoningLevel, meta.SupportedReasoningLevels,
		meta.SupportsReasoningSummaries, meta.DefaultReasoningSummary,
		meta.BaseInstructions)
}

// BuildModelInfosFromConfig returns Codex model catalog entries. Provider model
// catalogs are the primary source; routes are appended as fallback aliases.
func BuildModelInfosFromConfig(cfg config.Config) []ModelInfo {
	seen := make(map[string]bool)
	var models []ModelInfo

	providerKeys := make([]string, 0, len(cfg.ProviderDefs))
	for key := range cfg.ProviderDefs {
		providerKeys = append(providerKeys, key)
	}
	sort.Strings(providerKeys)
	for _, providerKey := range providerKeys {
		def := cfg.ProviderDefs[providerKey]
		modelNames := make([]string, 0, len(def.Models))
		for name := range def.Models {
			modelNames = append(modelNames, name)
		}
		sort.Strings(modelNames)
		for _, name := range modelNames {
			slug := name + "(" + providerKey + ")"
			if seen[slug] {
				continue
			}
			seen[slug] = true
			models = append(models, BuildModelInfoFromProviderModel(slug, providerKey, def.Models[name]))
		}
	}

	routeAliases := make([]string, 0, len(cfg.Routes))
	for alias := range cfg.Routes {
		routeAliases = append(routeAliases, alias)
	}
	sort.Strings(routeAliases)
	for _, alias := range routeAliases {
		if seen[alias] {
			continue
		}
		seen[alias] = true
		route := cfg.Routes[alias]
		ownedBy := "system"
		if route.Provider != "" {
			ownedBy = route.Provider
		}
		models = append(models, BuildModelInfoFromRoute(alias, ownedBy, route))
	}

	return models
}

// newModelInfo builds a ModelInfo with all fields Codex requires.
func newModelInfo(
	slug, displayName, description string,
	contextWindow int,
	defaultReasoningLevel string,
	supportedLevels []config.ReasoningLevelPreset,
	supportsReasoningSummaries bool,
	defaultReasoningSummary string,
	baseInstructions string,
) ModelInfo {
	var levels []ReasoningLevelPresetDTO
	for _, p := range supportedLevels {
		levels = append(levels, ReasoningLevelPresetDTO{Effort: p.Effort, Description: p.Description})
	}
	if levels == nil {
		levels = []ReasoningLevelPresetDTO{}
	}
	var ctxWin, maxCtxWin *int
	if contextWindow > 0 {
		v := contextWindow
		ctxWin = &v
		maxCtxWin = &v
	}
	if defaultReasoningSummary == "" {
		defaultReasoningSummary = "none"
	}
	applyPatchToolType := defaultApplyPatchToolType
	if baseInstructions == "" {
		baseInstructions = defaultBaseInstructions(slug)
	}
	return ModelInfo{
		Slug:                       slug,
		DisplayName:                displayName,
		Description:                description,
		DefaultReasoningLevel:      defaultReasoningLevel,
		SupportedReasoningLevels:   levels,
		ShellType:                  "unified_exec",
		Visibility:                 "list",
		SupportedInAPI:             true,
		Priority:                   0,
		AdditionalSpeedTiers:       []string{},
		BaseInstructions:           baseInstructions,
		SupportsReasoningSummaries: supportsReasoningSummaries,
		DefaultReasoningSummary:    defaultReasoningSummary,
		WebSearchToolType:          "text",
		ApplyPatchToolType:         &applyPatchToolType,
		TruncationPolicy:           truncationPolicyForModel(slug),
		SupportsParallelToolCalls:  true,
		ContextWindow:              ctxWin,
		MaxContextWindow:           maxCtxWin,
		EffectiveContextWindowPct:  95,
		ExperimentalSupportedTools: []string{},
		InputModalities:            []string{"text"},
	}
}

func truncationPolicyForModel(string) TruncationPolicyConfig {
	return TruncationPolicyConfig{Mode: "tokens", Limit: DefaultCatalogTruncationLimit}
}

// WriteModelsCatalog generates a Codex-compatible models_catalog.json from
// provider model catalogs, with routes appended as fallback aliases.
func WriteModelsCatalog(path string, cfg config.Config) error {
	catalog := struct {
		Models []ModelInfo `json:"models"`
	}{Models: BuildModelInfosFromConfig(cfg)}
	data, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func valueOrDefault(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// GenerateConfigToml writes a Codex config.toml fragment to output for a given
// model alias. If codexHome is non-empty, it also writes models_catalog.json
// there and adds a model_catalog_json pointer.
func GenerateConfigToml(output io.Writer, modelAlias string, baseURL string, codexHome string, cfg config.Config) error {
	route := cfg.RouteFor(modelAlias)

	// Transform "provider/model" format to "model(provider)" for Codex display.
	if provider, modelName := config.ParseModelRef(modelAlias); provider != "" {
		modelAlias = modelName + "(" + provider + ")"
	}
	fmt.Fprintf(output, "model = %q\n", modelAlias)
	fmt.Fprintln(output, `model_provider = "moonbridge"`)
	if route.ContextWindow > 0 {
		fmt.Fprintf(output, "model_context_window = %d\n", route.ContextWindow)
	}
	if route.MaxOutputTokens > 0 {
		fmt.Fprintf(output, "model_max_output_tokens = %d\n", route.MaxOutputTokens)
	}

	// Write models catalog JSON so Codex uses our metadata instead of bundled presets.
	if codexHome != "" {
		catalogPath := filepath.Join(codexHome, "models_catalog.json")
		if err := WriteModelsCatalog(catalogPath, cfg); err != nil {
			return fmt.Errorf("write models catalog: %w", err)
		}
		fmt.Fprintf(output, "model_catalog_json = %q\n", catalogPath)
	}

	fmt.Fprintln(output)
	fmt.Fprintln(output, "[model_providers.moonbridge]")
	fmt.Fprintln(output, `name = "Moon Bridge"`)
	fmt.Fprintf(output, "base_url = %q\n", valueOrDefault(baseURL, "http://"+config.DefaultAddr+"/v1"))
	fmt.Fprintln(output, `env_key = "MOONBRIDGE_CLIENT_API_KEY"`)
	fmt.Fprintln(output, `wire_api = "responses"`)
	fmt.Fprintln(output)
	fmt.Fprintln(output, "[mcp_servers.deepwiki]")
	fmt.Fprintln(output, `url = "https://mcp.deepwiki.com/mcp"`)
	fmt.Fprintln(output, "startup_timeout_sec = 3600")
	fmt.Fprintln(output, "tool_timeout_sec = 3600")
	return nil
}
