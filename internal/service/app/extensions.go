package app

import (
	"log/slog"

	deepseekv4 "moonbridge/internal/extension/deepseek_v4"
	"moonbridge/internal/extension/plugin"
	"moonbridge/internal/extension/visual"
	"moonbridge/internal/foundation/config"
)

type BuiltinExtensionCatalog struct{}

func BuiltinExtensions() BuiltinExtensionCatalog {
	return BuiltinExtensionCatalog{}
}

func (BuiltinExtensionCatalog) ConfigSpecs() []config.ExtensionConfigSpec {
	var specs []config.ExtensionConfigSpec
	specs = append(specs, deepseekv4.ConfigSpecs()...)
	specs = append(specs, visual.ConfigSpecs()...)
	return specs
}

func (BuiltinExtensionCatalog) NewRegistry(logger *slog.Logger, cfg config.Config) *plugin.Registry {
	registry := plugin.NewRegistry(logger)
	registry.Register(deepseekv4.NewPlugin())
	registry.Register(visual.NewPlugin())
	return registry
}
