package configgraph

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/ZhiYi-R/moon-bridge/internal/config"
	runtimepkg "github.com/ZhiYi-R/moon-bridge/internal/service/runtime"
)

type Store interface {
	LoadAll() (*config.Config, error)
	SaveConfig(context.Context, *config.Config) (string, error)
	CurrentRevision() (string, error)
}

type Runtime interface {
	Current() *runtimepkg.ConfigSnapshot
	ValidateCandidate(config.Config) error
	Reload(config.Config) error
}

type Service struct {
	store          Store
	runtime        Runtime
	logger         *slog.Logger
	extensionSpecs []config.ExtensionConfigSpec
}

func NewService(store Store, rt Runtime, logger *slog.Logger) *Service {
	if store == nil {
		panic("configgraph service store is nil")
	}
	if rt == nil {
		panic("configgraph service runtime is nil")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{store: store, runtime: rt, logger: logger}
}

func (s *Service) WithExtensionSpecs(specs []config.ExtensionConfigSpec) *Service {
	s.extensionSpecs = append([]config.ExtensionConfigSpec(nil), specs...)
	return s
}

func (s *Service) Graph(context.Context) (Graph, error) {
	revision, err := s.store.CurrentRevision()
	if err != nil {
		return Graph{}, fmt.Errorf("config graph revision: %w", err)
	}
	snapshot := s.runtime.Current()
	if snapshot == nil {
		return Graph{}, errors.New("config graph runtime snapshot is nil")
	}
	return BuildGraph(snapshot.Config, revision), nil
}

func (s *Service) Patch(ctx context.Context, req PatchRequest) (PatchResponse, error) {
	revision, err := s.store.CurrentRevision()
	if err != nil {
		return PatchResponse{}, fmt.Errorf("config graph revision: %w", err)
	}
	if req.BaseRevision == "" || req.BaseRevision != revision {
		return PatchResponse{
			Result:   ResultRevisionConflict,
			Revision: revision,
			Errors: []FieldError{
				{
					Code:    "revisionConflict",
					Message: fmt.Sprintf("base revision %q does not match current revision %q", req.BaseRevision, revision),
				},
			},
		}, nil
	}
	if len(req.Changes) == 0 {
		graph, err := s.Graph(ctx)
		if err != nil {
			return PatchResponse{}, err
		}
		return PatchResponse{Result: ResultCommitted, Revision: revision, Graph: &graph}, nil
	}

	cfg, err := s.store.LoadAll()
	if err != nil {
		return PatchResponse{}, fmt.Errorf("load config for graph patch: %w", err)
	}
	patched, errs := ApplyPatchToFileConfig(cfg.ToFileConfig(), req.Changes)
	if len(errs) > 0 {
		return PatchResponse{
			Result:   ResultValidationRejected,
			Revision: revision,
			Errors:   errs,
		}, nil
	}
	return s.acceptCandidate(ctx, patched, revision, req.Changes, true)
}

func (s *Service) Validate(ctx context.Context, req PatchRequest) (PatchResponse, error) {
	revision, err := s.store.CurrentRevision()
	if err != nil {
		return PatchResponse{}, fmt.Errorf("config graph revision: %w", err)
	}
	if req.BaseRevision == "" || req.BaseRevision != revision {
		return PatchResponse{
			Result:   ResultRevisionConflict,
			Revision: revision,
			Errors: []FieldError{
				{
					Code:    "revisionConflict",
					Message: fmt.Sprintf("base revision %q does not match current revision %q", req.BaseRevision, revision),
				},
			},
		}, nil
	}

	cfg, err := s.store.LoadAll()
	if err != nil {
		return PatchResponse{}, fmt.Errorf("load config for graph validation: %w", err)
	}
	patched, errs := ApplyPatchToFileConfig(cfg.ToFileConfig(), req.Changes)
	if len(errs) > 0 {
		return PatchResponse{Result: ResultValidationRejected, Revision: revision, Errors: errs}, nil
	}
	return s.acceptCandidate(ctx, patched, revision, req.Changes, false)
}

func (s *Service) CreateResource(ctx context.Context, kind ResourceKind, id string, value map[string]any) (PatchResponse, error) {
	revision, err := s.store.CurrentRevision()
	if err != nil {
		return PatchResponse{}, fmt.Errorf("config graph revision: %w", err)
	}
	cfg, err := s.store.LoadAll()
	if err != nil {
		return PatchResponse{}, fmt.Errorf("load config for graph create: %w", err)
	}
	fc := cfg.ToFileConfig()
	if err := createResource(&fc, kind, id, value); err != nil {
		return PatchResponse{Result: ResultValidationRejected, Revision: revision, Errors: []FieldError{*err}}, nil
	}
	return s.acceptCandidate(ctx, fc, revision, []PatchOp{{Kind: kind, ID: id}}, true)
}

func (s *Service) DeleteResource(ctx context.Context, kind ResourceKind, id string, baseRevision string) (PatchResponse, error) {
	revision, err := s.store.CurrentRevision()
	if err != nil {
		return PatchResponse{}, fmt.Errorf("config graph revision: %w", err)
	}
	if baseRevision == "" || baseRevision != revision {
		return PatchResponse{
			Result:   ResultRevisionConflict,
			Revision: revision,
			Errors: []FieldError{
				{
					ResourceKind: kind,
					ResourceID:   id,
					Code:         "revisionConflict",
					Message:      fmt.Sprintf("base revision %q does not match current revision %q", baseRevision, revision),
				},
			},
		}, nil
	}
	cfg, err := s.store.LoadAll()
	if err != nil {
		return PatchResponse{}, fmt.Errorf("load config for graph delete: %w", err)
	}
	fc := cfg.ToFileConfig()
	if err := deleteResource(&fc, kind, id); err != nil {
		return PatchResponse{Result: ResultValidationRejected, Revision: revision, Errors: []FieldError{*err}}, nil
	}
	return s.acceptCandidate(ctx, fc, revision, []PatchOp{{Kind: kind, ID: id}}, true)
}

func (s *Service) acceptCandidate(ctx context.Context, fc config.FileConfig, revision string, ops []PatchOp, commit bool) (PatchResponse, error) {
	candidate, errs := s.runtimeConfigFromFileConfig(fc, ops)
	if len(errs) > 0 {
		return PatchResponse{Result: ResultValidationRejected, Revision: revision, Errors: errs}, nil
	}
	if err := s.runtime.ValidateCandidate(candidate); err != nil {
		result := ResultDraftRejected
		if hasCriticalRuntimeImpact(ops) {
			result = ResultRuntimeRejected
		}
		return PatchResponse{
			Result:   result,
			Revision: revision,
			Errors: []FieldError{
				runtimeFieldError(ops, "runtimeCandidateRejected", err),
			},
		}, nil
	}
	if !commit {
		graph := BuildGraph(candidate, revision)
		return PatchResponse{Result: ResultCommitted, Revision: revision, Graph: &graph}, nil
	}

	nextRevision, err := s.store.SaveConfig(ctx, &candidate)
	if err != nil {
		return PatchResponse{}, fmt.Errorf("save config graph: %w", err)
	}
	if requiresRestart(ops) {
		graph := BuildGraph(candidate, nextRevision)
		return PatchResponse{Result: ResultRestartRequired, Revision: nextRevision, Graph: &graph}, nil
	}
	if err := s.runtime.Reload(candidate); err != nil {
		s.logger.Error("config graph runtime reload failed after committed save", "revision", nextRevision, "error", err)
		return PatchResponse{
			Result:   ResultRuntimeRejected,
			Revision: nextRevision,
			Graph:    graphPtr(BuildGraph(candidate, nextRevision)),
			Errors: []FieldError{
				runtimeFieldError(ops, "runtimeReloadRejected", err),
			},
		}, nil
	}
	graph := BuildGraph(candidate, nextRevision)
	return PatchResponse{Result: ResultCommitted, Revision: nextRevision, Graph: &graph}, nil
}

func (s *Service) runtimeConfigFromFileConfig(fc config.FileConfig, ops []PatchOp) (config.Config, []FieldError) {
	cfg, err := config.FromFileConfigWithOptions(fc, config.LoadOptions{ExtensionSpecs: s.extensionSpecs})
	if err != nil {
		return config.Config{}, []FieldError{runtimeFieldError(ops, "configValidationRejected", err)}
	}
	return cfg, nil
}

func createResource(fc *config.FileConfig, kind ResourceKind, id string, value map[string]any) *FieldError {
	op := PatchOp{Kind: kind, ID: id}
	if id == "" {
		return patchError(op, "invalidResourceID", "resource id cannot be empty")
	}
	switch kind {
	case ResourceModel:
		if fc.Models == nil {
			fc.Models = map[string]config.ModelDefFileConfig{}
		}
		if _, exists := fc.Models[id]; exists {
			return patchError(op, "duplicateResource", fmt.Sprintf("model %q already exists", id))
		}
		model, err := decodePatchValue[config.ModelDefFileConfig](value)
		if err != nil {
			return invalidValue(op, err)
		}
		fc.Models[id] = model
	case ResourceProvider:
		if fc.Providers == nil {
			fc.Providers = map[string]config.ProviderDefFileConfig{}
		}
		if _, exists := fc.Providers[id]; exists {
			return patchError(op, "duplicateResource", fmt.Sprintf("provider %q already exists", id))
		}
		provider, err := decodePatchValue[config.ProviderDefFileConfig](value)
		if err != nil {
			return invalidValue(op, err)
		}
		fc.Providers[id] = provider
	case ResourceProviderOffer:
		providerID, modelID, ok := splitProviderOfferID(id)
		if !ok {
			return patchError(op, "invalidResourceID", fmt.Sprintf("invalid provider offer id %q", id))
		}
		provider, exists := fc.Providers[providerID]
		if !exists {
			return patchError(op, "unknownResource", fmt.Sprintf("provider %q does not exist", providerID))
		}
		if offerIndex(provider.Offers, modelID) >= 0 {
			return patchError(op, "duplicateResource", fmt.Sprintf("provider offer %q already exists", id))
		}
		offer, err := decodePatchValue[config.OfferFileConfig](value)
		if err != nil {
			return invalidValue(op, err)
		}
		if offer.Model == "" {
			offer.Model = modelID
		}
		provider.Offers = append(provider.Offers, offer)
		fc.Providers[providerID] = provider
	case ResourceRoute:
		if fc.Routes == nil {
			fc.Routes = map[string]config.RouteFileConfig{}
		}
		if _, exists := fc.Routes[id]; exists {
			return patchError(op, "duplicateResource", fmt.Sprintf("route %q already exists", id))
		}
		route, err := decodePatchValue[config.RouteFileConfig](value)
		if err != nil {
			return invalidValue(op, err)
		}
		fc.Routes[id] = route
	case ResourceExtension:
		if fc.Extensions == nil {
			fc.Extensions = map[string]config.ExtensionFileConfig{}
		}
		if _, exists := fc.Extensions[id]; exists {
			return patchError(op, "duplicateResource", fmt.Sprintf("extension %q already exists", id))
		}
		extension, err := decodePatchValue[config.ExtensionFileConfig](value)
		if err != nil {
			return invalidValue(op, err)
		}
		fc.Extensions[id] = extension
	default:
		return patchError(op, "unsupportedCreate", fmt.Sprintf("resource kind %q cannot be created", kind))
	}
	return nil
}

func deleteResource(fc *config.FileConfig, kind ResourceKind, id string) *FieldError {
	op := PatchOp{Kind: kind, ID: id}
	switch kind {
	case ResourceModel:
		if _, exists := fc.Models[id]; !exists {
			return patchError(op, "unknownResource", fmt.Sprintf("model %q does not exist", id))
		}
		delete(fc.Models, id)
	case ResourceProvider:
		if _, exists := fc.Providers[id]; !exists {
			return patchError(op, "unknownResource", fmt.Sprintf("provider %q does not exist", id))
		}
		delete(fc.Providers, id)
	case ResourceProviderOffer:
		providerID, modelID, ok := splitProviderOfferID(id)
		if !ok {
			return patchError(op, "invalidResourceID", fmt.Sprintf("invalid provider offer id %q", id))
		}
		provider, exists := fc.Providers[providerID]
		if !exists {
			return patchError(op, "unknownResource", fmt.Sprintf("provider %q does not exist", providerID))
		}
		idx := offerIndex(provider.Offers, modelID)
		if idx < 0 {
			return patchError(op, "unknownResource", fmt.Sprintf("provider offer %q does not exist", id))
		}
		provider.Offers = append(provider.Offers[:idx], provider.Offers[idx+1:]...)
		fc.Providers[providerID] = provider
	case ResourceRoute:
		if _, exists := fc.Routes[id]; !exists {
			return patchError(op, "unknownResource", fmt.Sprintf("route %q does not exist", id))
		}
		delete(fc.Routes, id)
	case ResourceExtension:
		if _, exists := fc.Extensions[id]; !exists {
			return patchError(op, "unknownResource", fmt.Sprintf("extension %q does not exist", id))
		}
		delete(fc.Extensions, id)
	default:
		return patchError(op, "unsupportedDelete", fmt.Sprintf("resource kind %q cannot be deleted", kind))
	}
	return nil
}

func requiresRestart(ops []PatchOp) bool {
	for _, op := range ops {
		def, field, ok := definitionField(op.Kind, op.Field)
		if !ok {
			if def.Kind != "" && !def.HotReloadable {
				return true
			}
			continue
		}
		if !def.HotReloadable || !field.HotReloadable {
			return true
		}
	}
	return false
}

func hasCriticalRuntimeImpact(ops []PatchOp) bool {
	for _, op := range ops {
		def, field, ok := definitionField(op.Kind, op.Field)
		if ok {
			if field.RuntimeImpact == string(ImpactCritical) {
				return true
			}
			continue
		}
		if def.RuntimeImpact == ImpactCritical {
			return true
		}
	}
	return false
}

func definitionField(kind ResourceKind, fieldPath string) (ResourceDefinition, FieldSchema, bool) {
	def, exists := definitionsByKind()[kind]
	if !exists {
		return ResourceDefinition{}, FieldSchema{}, false
	}
	for _, field := range def.Fields {
		if field.Path == fieldPath {
			return def, field, true
		}
	}
	return def, FieldSchema{}, false
}

func runtimeFieldError(ops []PatchOp, code string, err error) FieldError {
	out := FieldError{Code: code, Message: err.Error()}
	if len(ops) > 0 {
		out.ResourceKind = ops[0].Kind
		out.ResourceID = ops[0].ID
		out.Field = ops[0].Field
	}
	return out
}

func graphPtr(graph Graph) *Graph {
	return &graph
}
