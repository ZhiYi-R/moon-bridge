package configgraph

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/ZhiYi-R/moon-bridge/internal/config"
)

const (
	mainResourceID = "main"
	secretMask     = "******"
)

func BuildGraph(cfg config.Config, revision string) Graph {
	fc := cfg.ToFileConfig()
	builder := graphBuilder{definitions: definitionsByKind()}

	resources := []Resource{
		builder.resource(ResourceMode, mainResourceID, "Mode", map[string]any{"mode": fc.Mode}, nil),
		builder.resource(ResourceTrace, mainResourceID, "Trace", mapFromValue(fc.Trace), nil),
		builder.resource(ResourceLog, mainResourceID, "Log", mapFromValue(fc.Log), nil),
		builder.resource(ResourceServer, mainResourceID, "Server", maskKeys(mapFromValue(fc.Server), "auth_token"), nil),
		builder.resource(ResourceDefaults, mainResourceID, "Defaults", mapFromValue(fc.Defaults), nil),
	}

	for _, slug := range sortedKeys(fc.Models) {
		resources = append(resources, builder.resource(ResourceModel, slug, labelOrID(fc.Models[slug].DisplayName, slug), mapFromValue(fc.Models[slug]), nil))
	}

	for _, key := range sortedKeys(fc.Providers) {
		providerValue := maskKeys(mapFromValue(fc.Providers[key]), "api_key")
		delete(providerValue, "offers")
		resources = append(resources, builder.resource(ResourceProvider, key, key, providerValue, nil))
		for _, offer := range fc.Providers[key].Offers {
			id := fmt.Sprintf("%s/%s", key, offer.Model)
			resources = append(resources, builder.resource(ResourceProviderOffer, id, id, mapFromValue(offer), []ResourceRef{
				{Kind: ResourceProvider, ID: key},
				{Kind: ResourceModel, ID: offer.Model},
			}))
		}
	}

	for _, alias := range sortedKeys(fc.Routes) {
		route := fc.Routes[alias]
		resources = append(resources, builder.resource(ResourceRoute, alias, labelOrID(route.DisplayName, alias), mapFromValue(route), []ResourceRef{
			{Kind: ResourceProvider, ID: route.Provider},
			{Kind: ResourceModel, ID: route.Model},
		}))
	}

	resources = append(resources,
		builder.resource(ResourceWebSearch, mainResourceID, "Web Search", maskKeys(mapFromValue(fc.WebSearch), "tavily_api_key", "firecrawl_api_key"), nil),
		builder.resource(ResourceCache, mainResourceID, "Cache", mapFromValue(fc.Cache), nil),
		builder.resource(ResourcePersistence, mainResourceID, "Persistence", mapFromValue(fc.Persistence), nil),
		builder.resource(ResourceProxy, mainResourceID, "Proxy", maskNestedKeys(mapFromValue(fc.Proxy), "api_key"), nil),
	)

	for _, name := range sortedKeys(fc.Extensions) {
		resources = append(resources, builder.resource(ResourceExtension, name, name, mapFromValue(fc.Extensions[name]), nil))
	}

	return Graph{
		Revision:  revision,
		Resources: resources,
		Validation: ValidationState{
			Valid: true,
		},
		Runtime: RuntimeState{
			Status: "ok",
		},
		Capabilities: Capabilities{
			Autosave: true,
			Logs:     true,
		},
	}
}

type graphBuilder struct {
	definitions map[ResourceKind]ResourceDefinition
}

func (b graphBuilder) resource(kind ResourceKind, id, label string, value map[string]any, refs []ResourceRef) Resource {
	def := b.definitions[kind]
	return Resource{
		Kind:          kind,
		ID:            id,
		Label:         label,
		Value:         value,
		Schema:        ResourceSchema{Fields: def.Fields},
		Status:        StatusSaved,
		RuntimeImpact: def.RuntimeImpact,
		HotReloadable: def.HotReloadable,
		References:    refs,
	}
}

func definitionsByKind() map[ResourceKind]ResourceDefinition {
	definitions := ResourceDefinitions()
	out := make(map[ResourceKind]ResourceDefinition, len(definitions))
	for _, def := range definitions {
		out[ResourceKind(def.Kind)] = def
	}
	return out
}

func mapFromValue(value any) map[string]any {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("marshal graph value %T: %v", value, err))
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		panic(fmt.Sprintf("unmarshal graph value %T: %v", value, err))
	}
	if out == nil {
		return map[string]any{}
	}
	return out
}

func maskKeys(value map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		if secret, ok := value[key].(string); ok && secret != "" {
			value[key] = secretMask
		}
	}
	return value
}

func maskNestedKeys(value map[string]any, keys ...string) map[string]any {
	for _, item := range value {
		child, ok := item.(map[string]any)
		if !ok {
			continue
		}
		maskKeys(child, keys...)
	}
	return value
}

func sortedKeys[V any](items map[string]V) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func labelOrID(label, id string) string {
	if label != "" {
		return label
	}
	return id
}
