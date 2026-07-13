package configgraph

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/ZhiYi-R/moon-bridge/internal/config"
)

func ApplyPatchToFileConfig(fc config.FileConfig, ops []PatchOp) (config.FileConfig, []FieldError) {
	candidate := cloneFileConfig(fc)
	schema := patchSchema()

	var errs []FieldError
	for _, op := range ops {
		if err := validatePatchTarget(candidate, schema, op); err != nil {
			errs = append(errs, *err)
			continue
		}
		if err := applyPatchOp(&candidate, op); err != nil {
			errs = append(errs, *err)
		}
	}
	if len(errs) > 0 {
		return fc, errs
	}
	return candidate, nil
}

func patchSchema() map[ResourceKind]map[string]bool {
	out := make(map[ResourceKind]map[string]bool)
	for _, def := range ResourceDefinitions() {
		fields := make(map[string]bool, len(def.Fields))
		for _, field := range def.Fields {
			fields[field.Path] = true
		}
		out[ResourceKind(def.Kind)] = fields
	}
	return out
}

func validatePatchTarget(fc config.FileConfig, schema map[ResourceKind]map[string]bool, op PatchOp) *FieldError {
	fields, ok := schema[op.Kind]
	if !ok {
		return patchError(op, "unknownKind", fmt.Sprintf("unknown resource kind %q", op.Kind))
	}
	if !fields[op.Field] {
		return patchError(op, "unknownField", fmt.Sprintf("unknown field %q for %s", op.Field, op.Kind))
	}
	if !resourceExists(fc, op.Kind, op.ID) {
		return patchError(op, "unknownResource", fmt.Sprintf("unknown resource %s/%s", op.Kind, op.ID))
	}
	return nil
}

func resourceExists(fc config.FileConfig, kind ResourceKind, id string) bool {
	switch kind {
	case ResourceMode, ResourceTrace, ResourceLog, ResourceServer, ResourceDefaults, ResourceWebSearch, ResourceCache, ResourcePersistence, ResourceProxy:
		return id == mainResourceID
	case ResourceModel:
		_, ok := fc.Models[id]
		return ok
	case ResourceProvider:
		_, ok := fc.Providers[id]
		return ok
	case ResourceProviderOffer:
		providerID, modelID, ok := splitProviderOfferID(id)
		if !ok {
			return false
		}
		provider, ok := fc.Providers[providerID]
		if !ok {
			return false
		}
		return offerIndex(provider.Offers, modelID) >= 0
	case ResourceRoute:
		_, ok := fc.Routes[id]
		return ok
	case ResourceExtension:
		_, ok := fc.Extensions[id]
		return ok
	default:
		return false
	}
}

func applyPatchOp(fc *config.FileConfig, op PatchOp) *FieldError {
	switch op.Kind {
	case ResourceMode:
		return applyModePatch(fc, op)
	case ResourceTrace:
		return applyTracePatch(fc, op)
	case ResourceLog:
		return applyLogPatch(fc, op)
	case ResourceServer:
		return applyServerPatch(fc, op)
	case ResourceDefaults:
		return applyDefaultsPatch(fc, op)
	case ResourceModel:
		return applyModelPatch(fc, op)
	case ResourceProvider:
		return applyProviderPatch(fc, op)
	case ResourceProviderOffer:
		return applyProviderOfferPatch(fc, op)
	case ResourceRoute:
		return applyRoutePatch(fc, op)
	case ResourceWebSearch:
		return applyWebSearchPatch(&fc.WebSearch, op, fc.WebSearch)
	case ResourceCache:
		return applyCachePatch(fc, op)
	case ResourcePersistence:
		return applyPersistencePatch(fc, op)
	case ResourceExtension:
		return applyExtensionPatch(fc, op)
	case ResourceProxy:
		return applyProxyPatch(fc, op)
	default:
		return patchError(op, "unknownKind", fmt.Sprintf("unknown resource kind %q", op.Kind))
	}
}

func applyModePatch(fc *config.FileConfig, op PatchOp) *FieldError {
	value, err := stringValue(op.Value)
	if err != nil {
		return invalidValue(op, err)
	}
	fc.Mode = value
	return nil
}

func applyTracePatch(fc *config.FileConfig, op PatchOp) *FieldError {
	value, err := boolValue(op.Value)
	if err != nil {
		return invalidValue(op, err)
	}
	fc.Trace.Enabled = value
	return nil
}

func applyLogPatch(fc *config.FileConfig, op PatchOp) *FieldError {
	value, err := stringValue(op.Value)
	if err != nil {
		return invalidValue(op, err)
	}
	switch op.Field {
	case "level":
		fc.Log.Level = value
	case "format":
		fc.Log.Format = value
	}
	return nil
}

func applyServerPatch(fc *config.FileConfig, op PatchOp) *FieldError {
	switch op.Field {
	case "addr":
		return setString(op, &fc.Server.Addr)
	case "auth_token":
		return setSecretString(op, &fc.Server.AuthToken)
	case "max_sessions":
		return setInt(op, &fc.Server.MaxSessions)
	case "session_ttl":
		return setString(op, &fc.Server.SessionTTL)
	default:
		return patchError(op, "unknownField", fmt.Sprintf("unknown server field %q", op.Field))
	}
}

func applyDefaultsPatch(fc *config.FileConfig, op PatchOp) *FieldError {
	switch op.Field {
	case "model":
		return setString(op, &fc.Defaults.Model)
	case "max_tokens":
		return setInt(op, &fc.Defaults.MaxTokens)
	case "system_prompt":
		return setString(op, &fc.Defaults.SystemPrompt)
	default:
		return patchError(op, "unknownField", fmt.Sprintf("unknown defaults field %q", op.Field))
	}
}

func applyModelPatch(fc *config.FileConfig, op PatchOp) *FieldError {
	model := fc.Models[op.ID]
	switch op.Field {
	case "context_window":
		if err := setInt(op, &model.ContextWindow); err != nil {
			return err
		}
	case "max_output_tokens":
		if err := setInt(op, &model.MaxOutputTokens); err != nil {
			return err
		}
	case "display_name":
		if err := setString(op, &model.DisplayName); err != nil {
			return err
		}
	case "description":
		if err := setString(op, &model.Description); err != nil {
			return err
		}
	case "base_instructions":
		if err := setString(op, &model.BaseInstructions); err != nil {
			return err
		}
	case "supports_reasoning":
		value, err := boolPtrValue(op.Value)
		if err != nil {
			return invalidValue(op, err)
		}
		model.SupportsReasoning = value
	case "default_reasoning_level":
		if err := setString(op, &model.DefaultReasoningLevel); err != nil {
			return err
		}
	case "supported_reasoning_levels":
		value, err := decodePatchValue[[]config.ReasoningLevelPresetFileConfig](op.Value)
		if err != nil {
			return invalidValue(op, err)
		}
		model.SupportedReasoningLevels = value
	case "supports_reasoning_summaries":
		value, err := boolPtrValue(op.Value)
		if err != nil {
			return invalidValue(op, err)
		}
		model.SupportsReasoningSummaries = value
	case "default_reasoning_summary":
		if err := setString(op, &model.DefaultReasoningSummary); err != nil {
			return err
		}
	case "input_modalities":
		value, err := decodePatchValue[[]string](op.Value)
		if err != nil {
			return invalidValue(op, err)
		}
		model.InputModalities = value
	case "supports_image_detail_original":
		value, err := boolPtrValue(op.Value)
		if err != nil {
			return invalidValue(op, err)
		}
		model.SupportsImageDetailOriginal = value
	case "web_search":
		value, err := decodePatchValue[config.WebSearchFileConfig](op.Value)
		if err != nil {
			return invalidValue(op, err)
		}
		model.WebSearch = preserveWebSearchSecrets(model.WebSearch, value)
	case "extensions":
		value, err := decodePatchValue[map[string]config.ExtensionFileConfig](op.Value)
		if err != nil {
			return invalidValue(op, err)
		}
		model.Extensions = value
	default:
		return patchError(op, "unknownField", fmt.Sprintf("unknown model field %q", op.Field))
	}
	fc.Models[op.ID] = model
	return nil
}

func applyProviderPatch(fc *config.FileConfig, op PatchOp) *FieldError {
	provider := fc.Providers[op.ID]
	switch op.Field {
	case "base_url":
		if err := setString(op, &provider.BaseURL); err != nil {
			return err
		}
	case "api_key":
		if err := setSecretString(op, &provider.APIKey); err != nil {
			return err
		}
	case "version":
		if err := setString(op, &provider.Version); err != nil {
			return err
		}
	case "user_agent":
		if err := setString(op, &provider.UserAgent); err != nil {
			return err
		}
	case "protocol":
		if err := setString(op, &provider.Protocol); err != nil {
			return err
		}
	case "web_search":
		value, err := decodePatchValue[config.WebSearchFileConfig](op.Value)
		if err != nil {
			return invalidValue(op, err)
		}
		provider.WebSearch = preserveWebSearchSecrets(provider.WebSearch, value)
	case "extensions":
		value, err := decodePatchValue[map[string]config.ExtensionFileConfig](op.Value)
		if err != nil {
			return invalidValue(op, err)
		}
		provider.Extensions = value
	default:
		return patchError(op, "unknownField", fmt.Sprintf("unknown provider field %q", op.Field))
	}
	fc.Providers[op.ID] = provider
	return nil
}

func applyProviderOfferPatch(fc *config.FileConfig, op PatchOp) *FieldError {
	providerID, modelID, ok := splitProviderOfferID(op.ID)
	if !ok {
		return patchError(op, "unknownResource", fmt.Sprintf("invalid provider offer id %q", op.ID))
	}
	provider := fc.Providers[providerID]
	idx := offerIndex(provider.Offers, modelID)
	if idx < 0 {
		return patchError(op, "unknownResource", fmt.Sprintf("unknown provider offer %q", op.ID))
	}
	offer := provider.Offers[idx]
	switch op.Field {
	case "model":
		if err := setString(op, &offer.Model); err != nil {
			return err
		}
	case "upstream_name":
		if err := setString(op, &offer.UpstreamName); err != nil {
			return err
		}
	case "priority":
		if err := setInt(op, &offer.Priority); err != nil {
			return err
		}
	case "pricing":
		value, err := decodePatchValue[config.ModelPricingFileConfig](op.Value)
		if err != nil {
			return invalidValue(op, err)
		}
		offer.Pricing = value
	case "overrides":
		value, err := decodePatchValue[*config.ModelDefFileConfig](op.Value)
		if err != nil {
			return invalidValue(op, err)
		}
		offer.Overrides = value
	default:
		return patchError(op, "unknownField", fmt.Sprintf("unknown provider offer field %q", op.Field))
	}
	provider.Offers[idx] = offer
	fc.Providers[providerID] = provider
	return nil
}

func applyRoutePatch(fc *config.FileConfig, op PatchOp) *FieldError {
	route := fc.Routes[op.ID]
	switch op.Field {
	case "to":
		if err := setString(op, &route.To); err != nil {
			return err
		}
	case "model":
		if err := setString(op, &route.Model); err != nil {
			return err
		}
	case "provider":
		if err := setString(op, &route.Provider); err != nil {
			return err
		}
	case "display_name":
		if err := setString(op, &route.DisplayName); err != nil {
			return err
		}
	case "description":
		if err := setString(op, &route.Description); err != nil {
			return err
		}
	case "context_window":
		if err := setInt(op, &route.ContextWindow); err != nil {
			return err
		}
	case "web_search":
		value, err := decodePatchValue[config.WebSearchFileConfig](op.Value)
		if err != nil {
			return invalidValue(op, err)
		}
		route.WebSearch = preserveWebSearchSecrets(route.WebSearch, value)
	case "extensions":
		value, err := decodePatchValue[map[string]config.ExtensionFileConfig](op.Value)
		if err != nil {
			return invalidValue(op, err)
		}
		route.Extensions = value
	default:
		return patchError(op, "unknownField", fmt.Sprintf("unknown route field %q", op.Field))
	}
	fc.Routes[op.ID] = route
	return nil
}

func applyWebSearchPatch(target *config.WebSearchFileConfig, op PatchOp, existing config.WebSearchFileConfig) *FieldError {
	switch op.Field {
	case "support":
		return setString(op, &target.Support)
	case "max_uses":
		return setInt(op, &target.MaxUses)
	case "tavily_api_key":
		if isSecretPlaceholder(op.Value) {
			target.TavilyAPIKey = existing.TavilyAPIKey
			return nil
		}
		return setString(op, &target.TavilyAPIKey)
	case "firecrawl_api_key":
		if isSecretPlaceholder(op.Value) {
			target.FirecrawlAPIKey = existing.FirecrawlAPIKey
			return nil
		}
		return setString(op, &target.FirecrawlAPIKey)
	case "search_max_rounds":
		return setInt(op, &target.SearchMaxRounds)
	default:
		return patchError(op, "unknownField", fmt.Sprintf("unknown web search field %q", op.Field))
	}
}

func applyCachePatch(fc *config.FileConfig, op PatchOp) *FieldError {
	switch op.Field {
	case "mode":
		return setString(op, &fc.Cache.Mode)
	case "ttl":
		return setString(op, &fc.Cache.TTL)
	case "prompt_caching":
		return setBoolPtr(op, &fc.Cache.PromptCaching)
	case "automatic_prompt_cache":
		return setBoolPtr(op, &fc.Cache.AutomaticPromptCache)
	case "explicit_cache_breakpoints":
		return setBoolPtr(op, &fc.Cache.ExplicitCacheBreakpoints)
	case "allow_retention_downgrade":
		return setBoolPtr(op, &fc.Cache.AllowRetentionDowngrade)
	case "max_breakpoints":
		return setInt(op, &fc.Cache.MaxBreakpoints)
	case "min_cache_tokens":
		return setInt(op, &fc.Cache.MinCacheTokens)
	case "expected_reuse":
		return setInt(op, &fc.Cache.ExpectedReuse)
	case "minimum_value_score":
		return setInt(op, &fc.Cache.MinimumValueScore)
	case "min_breakpoint_tokens":
		return setInt(op, &fc.Cache.MinBreakpointTokens)
	default:
		return patchError(op, "unknownField", fmt.Sprintf("unknown cache field %q", op.Field))
	}
}

func applyPersistencePatch(fc *config.FileConfig, op PatchOp) *FieldError {
	return setString(op, &fc.Persistence.ActiveProvider)
}

func applyExtensionPatch(fc *config.FileConfig, op PatchOp) *FieldError {
	extension := fc.Extensions[op.ID]
	switch op.Field {
	case "enabled":
		value, err := boolPtrValue(op.Value)
		if err != nil {
			return invalidValue(op, err)
		}
		extension.Enabled = value
	case "config":
		value, err := decodePatchValue[map[string]any](op.Value)
		if err != nil {
			return invalidValue(op, err)
		}
		extension.Config = value
	default:
		return patchError(op, "unknownField", fmt.Sprintf("unknown extension field %q", op.Field))
	}
	fc.Extensions[op.ID] = extension
	return nil
}

func applyProxyPatch(fc *config.FileConfig, op PatchOp) *FieldError {
	switch op.Field {
	case "response":
		value, err := decodePatchValue[config.ProxyTargetFileConfig](op.Value)
		if err != nil {
			return invalidValue(op, err)
		}
		fc.Proxy.Response = preserveProxySecret(fc.Proxy.Response, value)
	case "anthropic":
		value, err := decodePatchValue[config.ProxyTargetFileConfig](op.Value)
		if err != nil {
			return invalidValue(op, err)
		}
		fc.Proxy.Anthropic = preserveProxySecret(fc.Proxy.Anthropic, value)
	default:
		return patchError(op, "unknownField", fmt.Sprintf("unknown proxy field %q", op.Field))
	}
	return nil
}

func cloneFileConfig(fc config.FileConfig) config.FileConfig {
	raw, err := json.Marshal(fc)
	if err != nil {
		panic(fmt.Sprintf("marshal FileConfig clone: %v", err))
	}
	var out config.FileConfig
	if err := json.Unmarshal(raw, &out); err != nil {
		panic(fmt.Sprintf("unmarshal FileConfig clone: %v", err))
	}
	return out
}

func setString(op PatchOp, target *string) *FieldError {
	value, err := stringValue(op.Value)
	if err != nil {
		return invalidValue(op, err)
	}
	*target = value
	return nil
}

func setSecretString(op PatchOp, target *string) *FieldError {
	if isSecretPlaceholder(op.Value) {
		return nil
	}
	return setString(op, target)
}

func setInt(op PatchOp, target *int) *FieldError {
	value, err := intValue(op.Value)
	if err != nil {
		return invalidValue(op, err)
	}
	*target = value
	return nil
}

func setBoolPtr(op PatchOp, target **bool) *FieldError {
	value, err := boolPtrValue(op.Value)
	if err != nil {
		return invalidValue(op, err)
	}
	*target = value
	return nil
}

func stringValue(value any) (string, error) {
	v, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("expected string, got %T", value)
	}
	return v, nil
}

func boolValue(value any) (bool, error) {
	v, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("expected boolean, got %T", value)
	}
	return v, nil
}

func boolPtrValue(value any) (*bool, error) {
	if value == nil {
		return nil, nil
	}
	v, err := boolValue(value)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func intValue(value any) (int, error) {
	switch v := value.(type) {
	case int:
		return v, nil
	case int8:
		return int(v), nil
	case int16:
		return int(v), nil
	case int32:
		return int(v), nil
	case int64:
		return int(v), nil
	case uint:
		return int(v), nil
	case uint8:
		return int(v), nil
	case uint16:
		return int(v), nil
	case uint32:
		return int(v), nil
	case uint64:
		if v > math.MaxInt {
			return 0, fmt.Errorf("integer %d overflows int", v)
		}
		return int(v), nil
	case float64:
		if math.Trunc(v) != v {
			return 0, fmt.Errorf("expected integer, got %v", v)
		}
		if v > float64(math.MaxInt) || v < float64(math.MinInt) {
			return 0, fmt.Errorf("integer %v overflows int", v)
		}
		return int(v), nil
	case json.Number:
		i, err := strconv.Atoi(v.String())
		if err != nil {
			return 0, fmt.Errorf("expected integer, got %q", v.String())
		}
		return i, nil
	default:
		return 0, fmt.Errorf("expected integer, got %T", value)
	}
}

func decodePatchValue[T any](value any) (T, error) {
	var out T
	if value == nil {
		return out, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return out, fmt.Errorf("marshal patch value: %w", err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("decode patch value: %w", err)
	}
	return out, nil
}

func preserveWebSearchSecrets(existing, incoming config.WebSearchFileConfig) config.WebSearchFileConfig {
	if incoming.TavilyAPIKey == "" || incoming.TavilyAPIKey == secretMask {
		incoming.TavilyAPIKey = existing.TavilyAPIKey
	}
	if incoming.FirecrawlAPIKey == "" || incoming.FirecrawlAPIKey == secretMask {
		incoming.FirecrawlAPIKey = existing.FirecrawlAPIKey
	}
	return incoming
}

func preserveProxySecret(existing, incoming config.ProxyTargetFileConfig) config.ProxyTargetFileConfig {
	if incoming.APIKey == "" || incoming.APIKey == secretMask {
		incoming.APIKey = existing.APIKey
	}
	return incoming
}

func isSecretPlaceholder(value any) bool {
	text, ok := value.(string)
	if !ok {
		return false
	}
	return text == "" || text == secretMask
}

func splitProviderOfferID(id string) (string, string, bool) {
	providerID, modelID, ok := strings.Cut(id, "/")
	if !ok || providerID == "" || modelID == "" {
		return "", "", false
	}
	return providerID, modelID, true
}

func offerIndex(offers []config.OfferFileConfig, modelID string) int {
	for i, offer := range offers {
		if offer.Model == modelID {
			return i
		}
	}
	return -1
}

func invalidValue(op PatchOp, err error) *FieldError {
	return patchError(op, "invalidValue", err.Error())
}

func patchError(op PatchOp, code, message string) *FieldError {
	return &FieldError{
		ResourceKind: op.Kind,
		ResourceID:   op.ID,
		Field:        op.Field,
		Code:         code,
		Message:      message,
	}
}
