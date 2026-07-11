// Package codex provides Codex CLI model catalog DTOs and config generation.
//
// It is shared by internal/server and cmd/moonbridge to produce
// model catalog JSON and Codex config.toml fragments.
package codex

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"moonbridge/internal/config"
	"moonbridge/internal/extension/visual"
	"moonbridge/internal/modelref"
	"moonbridge/internal/util"
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

// DisplayNameFromSlug converts a slug like "gpt-5.5-codex" to "GPT 5.5 Codex".
func DisplayNameFromSlug(slug string) string {
	slug = strings.ReplaceAll(slug, "-", " ")
	words := strings.Fields(slug)
	for i, w := range words {
		lower := strings.ToLower(w)
		if isASCIIGPTPrefix(lower) {
			words[i] = "GPT" + w[3:]
			continue
		}
		words[i] = asciiTitle(w)
	}
	return strings.Join(words, " ")
}

// asciiTitle upper-cases the first ASCII letter and lower-cases the rest.
func asciiTitle(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + strings.ToLower(s[1:])
}

// isASCIIGPTPrefix reports whether s starts with "gpt" using ASCII byte match.
// It returns false for strings shorter than 3 bytes or non-ASCII prefixes.
func isASCIIGPTPrefix(s string) bool {
	if len(s) < 3 {
		return false
	}
	lower := strings.ToLower(s)
	return lower[:3] == "gpt"
}

// BuildModelInfoFromRoute creates a Codex-compatible ModelInfo from a route entry.
// ownedBy is kept for API compatibility but no longer affects displayName.
func BuildModelInfoFromRoute(alias string, ownedBy string, route config.RouteEntry) ModelInfo {
	displayName := route.DisplayName
	if displayName == "" {
		displayName = DisplayNameFromSlug(alias)
	}
	return newModelInfo(alias, displayName, route.Description, route.ContextWindow,
		route.SupportsReasoning,
		route.DefaultReasoningLevel, route.SupportedReasoningLevels,
		route.SupportsReasoningSummaries, route.DefaultReasoningSummary,
		route.BaseInstructions,
		inputModalitiesOrDefault(route.InputModalities),
		route.SupportsImageDetailOriginal)
}

// BuildModelInfoFromProviderModel creates a Codex-compatible ModelInfo from a
// provider model catalog entry. The slug is the upstream model name (pure, no
// provider suffix) and displayName is derived from the meta or auto-generated.
func BuildModelInfoFromProviderModel(slug string, ownedBy string, meta config.ModelMeta) ModelInfo {
	displayName := meta.DisplayName
	if displayName == "" {
		displayName = DisplayNameFromSlug(slug)
	}
	return newModelInfo(slug, displayName, meta.Description, meta.ContextWindow,
		meta.SupportsReasoning,
		meta.DefaultReasoningLevel, meta.SupportedReasoningLevels,
		meta.SupportsReasoningSummaries, meta.DefaultReasoningSummary,
		meta.BaseInstructions,
		inputModalitiesOrDefault(meta.InputModalities),
		meta.SupportsImageDetailOriginal)
}

// BuildModelInfosFromConfig returns Codex model catalog entries.
// Directly iterates provider models (canonical model definitions) and appends route aliases.
// Models without a provider offer or route are excluded — they are dead entries.
func BuildModelInfosFromConfig(providerCfg config.ProviderConfig, pluginCfg config.PluginConfig) []ModelInfo {
	// Build set of model names that have at least one provider offer.
	offeredModels := make(map[string]bool)
	for _, def := range providerCfg.Providers {
		for _, offer := range def.Offers {
			offeredModels[offer.Model] = true
		}
	}
	// Build set of model names targeted by routes.
	routeModels := make(map[string]bool)
	for _, route := range providerCfg.Routes {
		if route.Model != "" {
			routeModels[route.Model] = true
		}
	}
	// Filter: only include models that are offered by a provider or targeted by a route.
	modelSlugs := make([]string, 0, len(providerCfg.Models))
	for slug := range providerCfg.Models {
		if offeredModels[slug] || routeModels[slug] {
			modelSlugs = append(modelSlugs, slug)
		}
	}
	sort.Strings(modelSlugs)

	var models []ModelInfo
	for _, slug := range modelSlugs {
		def := providerCfg.Models[slug]
		displayName := def.DisplayName
		if displayName == "" {
			displayName = DisplayNameFromSlug(slug)
		}
		models = append(models, newModelInfo(
			slug,
			displayName,
			def.Description,
			def.ContextWindow,
			def.SupportsReasoning,
			def.DefaultReasoningLevel,
			def.SupportedReasoningLevels,
			def.SupportsReasoningSummaries,
			def.DefaultReasoningSummary,
			def.BaseInstructions,
			inputModalitiesOrDefault(def.InputModalities),
			def.SupportsImageDetailOriginal,
		))
	}

	// Fallback: if providerCfg.Models is empty, build from ProviderDefs Models (Phase 1 compat).
	if len(providerCfg.Models) == 0 {
		models = buildModelInfosFromProviderDefs(providerCfg)
	}

	// Route alias append (non-deduplicated model names only).
	routeAliases := make([]string, 0, len(providerCfg.Routes))
	for alias := range providerCfg.Routes {
		routeAliases = append(routeAliases, alias)
	}
	sort.Strings(routeAliases)
	seen := make(map[string]bool)
	for _, m := range models {
		seen[m.Slug] = true
	}
	for _, alias := range routeAliases {
		if seen[alias] {
			continue
		}
		seen[alias] = true
		route := providerCfg.Routes[alias]
		ownedBy := "system"
		if route.Provider != "" {
			ownedBy = route.Provider
		}
		models = append(models, BuildModelInfoFromRoute(alias, ownedBy, route))
	}

	models = injectVisualModalities(models, pluginCfg)
	return models
}

// buildModelInfosFromProviderDefs builds catalog entries from ProviderDef models.
// This is a fallback for Phase 1 when canonical Models map is not yet populated.
func buildModelInfosFromProviderDefs(providerCfg config.ProviderConfig) []ModelInfo {
	// First pass: group by upstream model name across providers.
	type providerEntry struct {
		key  string
		meta config.ModelMeta
	}
	grouped := make(map[string][]providerEntry)

	providerKeys := make([]string, 0, len(providerCfg.Providers))
	for key := range providerCfg.Providers {
		providerKeys = append(providerKeys, key)
	}
	sort.Strings(providerKeys)
	for _, providerKey := range providerKeys {
		def := providerCfg.Providers[providerKey]
		modelNames := make([]string, 0, len(def.Models))
		for name := range def.Models {
			modelNames = append(modelNames, name)
		}
		sort.Strings(modelNames)
		for _, name := range modelNames {
			grouped[name] = append(grouped[name], providerEntry{key: providerKey, meta: def.Models[name]})
		}
	}

	// Second pass: merge metadata for each model name deterministically.
	modelNames := make([]string, 0, len(grouped))
	for name := range grouped {
		modelNames = append(modelNames, name)
	}
	sort.Strings(modelNames)

	var models []ModelInfo
	for _, name := range modelNames {
		entries := grouped[name]
		sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
		preferred := entries[0]

		slug := name
		displayName := preferred.meta.DisplayName
		if displayName == "" {
			displayName = DisplayNameFromSlug(name)
		}

		description := preferred.meta.Description

		contextWindow := preferred.meta.ContextWindow
		for _, e := range entries[1:] {
			if e.meta.ContextWindow > contextWindow {
				contextWindow = e.meta.ContextWindow
			}
		}

		modalitySet := make(map[string]struct{})
		for _, e := range entries {
			for _, m := range e.meta.InputModalities {
				modalitySet[m] = struct{}{}
			}
		}
		mergedModalities := make([]string, 0, len(modalitySet))
		for m := range modalitySet {
			mergedModalities = append(mergedModalities, m)
		}
		sort.Strings(mergedModalities)

		seenEffort := make(map[string]bool)
		var mergedLevels []config.ReasoningLevelPreset
		for _, l := range preferred.meta.SupportedReasoningLevels {
			if !seenEffort[l.Effort] {
				seenEffort[l.Effort] = true
				mergedLevels = append(mergedLevels, l)
			}
		}
		for _, e := range entries[1:] {
			for _, l := range e.meta.SupportedReasoningLevels {
				if !seenEffort[l.Effort] {
					seenEffort[l.Effort] = true
					mergedLevels = append(mergedLevels, l)
				}
			}
		}
		if mergedLevels == nil {
			mergedLevels = []config.ReasoningLevelPreset{}
		}

		supportsReasoningSummaries := preferred.meta.SupportsReasoningSummaries
		for _, e := range entries[1:] {
			if e.meta.SupportsReasoningSummaries {
				supportsReasoningSummaries = true
			}
		}
		supportsReasoning := preferred.meta.SupportsReasoning
		for _, e := range entries[1:] {
			if e.meta.SupportsReasoning {
				supportsReasoning = true
			}
		}

		supportsImageDetailOriginal := preferred.meta.SupportsImageDetailOriginal
		for _, e := range entries[1:] {
			if e.meta.SupportsImageDetailOriginal {
				supportsImageDetailOriginal = true
			}
		}

		models = append(models, newModelInfo(
			slug,
			displayName,
			description,
			contextWindow,
			supportsReasoning,
			preferred.meta.DefaultReasoningLevel,
			mergedLevels,
			supportsReasoningSummaries,
			preferred.meta.DefaultReasoningSummary,
			preferred.meta.BaseInstructions,
			inputModalitiesOrDefault(mergedModalities),
			supportsImageDetailOriginal,
		))
	}
	return models
}

func injectVisualModalities(models []ModelInfo, pluginCfg config.PluginConfig) []ModelInfo {
	result := make([]ModelInfo, len(models))
	copy(result, models)
	for i, m := range result {
		if setting, ok := pluginCfg.Extensions[visual.PluginName]; ok && setting.Enabled != nil && *setting.Enabled {
			hasImage := false
			for _, mod := range m.InputModalities {
				if mod == "image" {
					hasImage = true
					break
				}
			}
			if !hasImage {
				// Append "image" to existing modalities; preserve any non-standard
				// modalities (e.g. "audio") the user may have configured.
				// Default to ["text"] if the list is empty.
				base := result[i].InputModalities
				if len(base) == 0 {
					base = []string{"text"}
				}
				result[i].InputModalities = append(base, "image")
			}
		}
	}
	return result
}

// newModelInfo builds a ModelInfo with all fields Codex requires.
func newModelInfo(
	slug, displayName, description string,
	contextWindow int,
	supportsReasoning bool,
	defaultReasoningLevel string,
	supportedLevels []config.ReasoningLevelPreset,
	supportsReasoningSummaries bool,
	defaultReasoningSummary string,
	baseInstructions string,
	inputModalities []string,
	supportsImageDetailOriginal bool,
) ModelInfo {
	if !supportsReasoning {
		defaultReasoningLevel = ""
		supportedLevels = nil
		supportsReasoningSummaries = false
		defaultReasoningSummary = ""
	}
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
		Slug:                        slug,
		DisplayName:                 displayName,
		Description:                 description,
		DefaultReasoningLevel:       defaultReasoningLevel,
		SupportedReasoningLevels:    levels,
		ShellType:                   "unified_exec",
		Visibility:                  "list",
		SupportedInAPI:              true,
		Priority:                    0,
		AdditionalSpeedTiers:        []string{},
		BaseInstructions:            baseInstructions,
		SupportsReasoningSummaries:  supportsReasoningSummaries,
		DefaultReasoningSummary:     defaultReasoningSummary,
		WebSearchToolType:           "text",
		ApplyPatchToolType:          &applyPatchToolType,
		TruncationPolicy:            truncationPolicyForModel(slug),
		SupportsParallelToolCalls:   true,
		ContextWindow:               ctxWin,
		MaxContextWindow:            maxCtxWin,
		EffectiveContextWindowPct:   95,
		ExperimentalSupportedTools:  []string{},
		InputModalities:             inputModalities,
		SupportsImageDetailOriginal: supportsImageDetailOriginal,
	}
}

func inputModalitiesOrDefault(modalities []string) []string {
	if len(modalities) == 0 {
		return []string{"text"}
	}
	return modalities
}

func truncationPolicyForModel(string) TruncationPolicyConfig {
	return TruncationPolicyConfig{Mode: "tokens", Limit: DefaultCatalogTruncationLimit}
}

// WriteModelsCatalog generates a Codex-compatible models_catalog.json from
// provider model catalogs, with routes appended as fallback aliases.
func WriteModelsCatalog(path string, providerCfg config.ProviderConfig, pluginCfg config.PluginConfig) error {
	catalog := struct {
		Models []ModelInfo `json:"models"`
	}{Models: BuildModelInfosFromConfig(providerCfg, pluginCfg)}
	data, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// routeFor resolves a model alias to a RouteEntry from a ProviderConfig.
func routeFor(providerCfg config.ProviderConfig, modelAlias string) config.RouteEntry {
	if provider, upstream := modelref.Parse(modelAlias); provider != "" {
		if def, ok := providerCfg.Providers[provider]; ok {
			entry := config.RouteEntry{Provider: provider, Model: upstream}
			if meta, ok := def.Models[upstream]; ok {
				entry.ContextWindow = meta.ContextWindow
				entry.MaxOutputTokens = meta.MaxOutputTokens
				entry.InputPrice = meta.InputPrice
				entry.OutputPrice = meta.OutputPrice
				entry.CacheWritePrice = meta.CacheWritePrice
				entry.CacheReadPrice = meta.CacheReadPrice
				entry.DisplayName = meta.DisplayName
				entry.Description = meta.Description
				entry.BaseInstructions = meta.BaseInstructions
				entry.DefaultReasoningLevel = meta.DefaultReasoningLevel
				entry.SupportedReasoningLevels = meta.SupportedReasoningLevels
				entry.SupportsReasoningSummaries = meta.SupportsReasoningSummaries
				entry.DefaultReasoningSummary = meta.DefaultReasoningSummary
				entry.InputModalities = meta.InputModalities
				entry.SupportsImageDetailOriginal = meta.SupportsImageDetailOriginal
				entry.WebSearch = meta.WebSearch
				entry.Extensions = meta.Extensions
			}
			return entry
		}
	}
	return providerCfg.Routes[modelAlias]
}

// GenerateConfigToml writes a Codex config.toml fragment to output for a given
// model alias. If codexHome is non-empty, it also writes models_catalog.json
// there and adds a model_catalog_json pointer.
func GenerateConfigToml(output io.Writer, modelAlias string, baseURL string, codexHome string, providerCfg config.ProviderConfig, pluginCfg config.PluginConfig, serverCfg config.ServerConfig) error {
	route := routeFor(providerCfg, modelAlias)

	// When modelAlias is a direct provider/model reference (not a named route),
	// normalize to model(provider) format so Codex can match it against catalog slugs.
	catalogAlias := modelAlias
	if _, isRoute := providerCfg.Routes[modelAlias]; !isRoute {
		if provider, model := modelref.Parse(modelAlias); provider != "" {
			catalogAlias = model + "(" + provider + ")"
		}
	}

	fmt.Fprintf(output, "model = %q\n", catalogAlias)
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
		if err := WriteModelsCatalog(catalogPath, providerCfg, pluginCfg); err != nil {
			return fmt.Errorf("write models catalog: %w", err)
		}
		fmt.Fprintf(output, "model_catalog_json = %q\n", catalogPath)
		if serverCfg.AuthToken != "" {
			if err := writeAuthJSON(filepath.Join(codexHome, "auth.json"), serverCfg.AuthToken); err != nil {
				return fmt.Errorf("write auth.json: %w", err)
			}
		}
	}

	fmt.Fprintln(output)
	fmt.Fprintln(output, "[model_providers.moonbridge]")
	fmt.Fprintln(output, `name = "Moon Bridge"`)
	fmt.Fprintf(output, "base_url = %q\n", util.OrDefault(baseURL, "http://"+config.DefaultAddr+"/v1"))
	if serverCfg.AuthToken != "" {
		fmt.Fprintln(output, `requires_openai_auth = true`)
	}
	fmt.Fprintln(output, `wire_api = "responses"`)
	fmt.Fprintln(output)
	fmt.Fprintln(output, "[mcp_servers.deepwiki]")
	fmt.Fprintln(output, `url = "https://mcp.deepwiki.com/mcp"`)
	fmt.Fprintln(output, "startup_timeout_sec = 3600")
	fmt.Fprintln(output, "tool_timeout_sec = 3600")
	return nil
}

// writeAuthJSON writes the API key into Codex's auth.json so that model_providers
// using requires_openai_auth can find the bearer token.
func writeAuthJSON(path, token string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(map[string]string{"openai_api_key": token})
}
