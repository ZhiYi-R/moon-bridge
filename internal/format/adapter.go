// Package format defines protocol-agnostic Core types for MoonBridge.
//
// This file defines the Adapter interfaces and CorePluginHooks that connect
// protocol-specific DTOs to the Core intermediate format.
//
// Clean room design: no imports from anthropic, openai, or any protocol-specific
// packages. Only Go standard library + the Core types defined in this package.
package format

import (
	"context"
	"encoding/json"
)

type coreRequestContextKey struct{}

// ContextWithCoreRequest returns a child context carrying the active CoreRequest.
// Hooks that only receive context can use CoreRequestFromContext to recover
// request-scoped metadata such as the model alias.
func ContextWithCoreRequest(ctx context.Context, req *CoreRequest) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, coreRequestContextKey{}, req)
}

// CoreRequestFromContext returns the CoreRequest previously stored in ctx.
func CoreRequestFromContext(ctx context.Context) (*CoreRequest, bool) {
	if ctx == nil {
		return nil, false
	}
	req, ok := ctx.Value(coreRequestContextKey{}).(*CoreRequest)
	return req, ok
}

// ============================================================================
// ClientAdapter — inbound protocol ↔ Core (inbound side of the bridge)
// ============================================================================

// ClientAdapter converts inbound client protocol requests/responses to/from Core format.
// Example: OpenAI Responses request → CoreRequest, CoreResponse → OpenAI Responses response.
type ClientAdapter interface {
	// ClientProtocol returns the inbound protocol identifier
	// (e.g. "openai-response").
	ClientProtocol() string

	// ToCoreRequest converts an inbound protocol request DTO into a CoreRequest.
	ToCoreRequest(ctx context.Context, req any) (*CoreRequest, error)

	// FromCoreResponse converts a CoreResponse back into the inbound protocol response DTO.
	FromCoreResponse(ctx context.Context, resp *CoreResponse) (any, error)
}

// ============================================================================
// ProviderAdapter — Core ↔ upstream protocol (upstream side of the bridge)
// ============================================================================

// ProviderAdapter converts Core format to/from an upstream provider protocol.
// Example: CoreRequest → anthropic.MessageRequest, anthropic.MessageResponse → CoreResponse.
type ProviderAdapter interface {
	// ProviderProtocol returns the upstream protocol identifier.
	// Must align with ProviderCandidate.Protocol (e.g. "anthropic").
	ProviderProtocol() string

	// FromCoreRequest converts a CoreRequest into an upstream protocol request DTO.
	// This is where protocol-specific transforms happen (e.g. cache injection).
	FromCoreRequest(ctx context.Context, req *CoreRequest) (any, error)

	// ToCoreResponse converts an upstream protocol response DTO into a CoreResponse.
	ToCoreResponse(ctx context.Context, resp any) (*CoreResponse, error)
}

// ============================================================================
// ClientStreamAdapter — Core stream events ↦ inbound protocol stream
// ============================================================================

// ClientStreamAdapter serializes Core stream events into the inbound protocol's
// streaming output format (e.g. OpenAI SSE).
type ClientStreamAdapter interface {
	// ClientProtocol returns the inbound protocol identifier.
	ClientProtocol() string

	// FromCoreStream consumes a channel of CoreStreamEvent and produces
	// the inbound protocol's stream representation.
	FromCoreStream(ctx context.Context, req *CoreRequest, events <-chan CoreStreamEvent) (any, error)
}

// ============================================================================
// ProviderStreamAdapter — upstream stream source ↦ Core stream events
// ============================================================================

// ProviderStreamAdapter consumes an upstream stream source and produces
// a channel of CoreStreamEvent.
//
// The adapter owns the read-loop — the caller receives events from the returned
// channel and is not responsible for iterating the upstream stream.
type ProviderStreamAdapter interface {
	// ProviderProtocol returns the upstream protocol identifier.
	ProviderProtocol() string

	// ToCoreStream consumes an upstream stream source (e.g. anthropic.Stream)
	// and returns a channel of CoreStreamEvent. The adapter is responsible for
	// the read-loop inside a goroutine.
	ToCoreStream(ctx context.Context, src any) (*StreamResult, error)
}

// ============================================================================

// StreamResult wraps the output of a streaming invocation with per-stream
// buffer access for trace capture and plugin state tracking.
//
// Using a concrete return type (rather than a bare channel) keeps the
// buffer local to each streaming call, eliminating the data race that
// occurred when buffers were stored on the shared adapter instance.
type StreamResult struct {
	// Events is the channel of CoreStreamEvent from the upstream stream.
	Events <-chan CoreStreamEvent

	// StreamBuffer returns the captured raw upstream events for trace,
	// plugin reasoning replay, and other post-stream processing.
	// Must only be called after the Events channel is fully consumed.
	StreamBuffer func() []any
}

// CorePluginHooks — protocol-agnostic plugin hooks operating on Core format
// ============================================================================

// CorePluginHooks provides plugin extension points that operate on Core format.
//
// This is a protocol-agnostic replacement for Bridge.PluginHooks — all method
// signatures use Core types (CoreRequest, CoreResponse, CoreStreamEvent, etc.)
// rather than protocol-specific types (anthropic.MessageRequest, etc.).
//
// During the migration period, both hook systems coexist: Bridge paths use the
// old PluginHooks, Adapter paths use CorePluginHooks.
type CorePluginHooks struct {
	// PreprocessInput preprocesses raw input before request conversion.
	PreprocessInput func(ctx context.Context, model string, raw json.RawMessage) json.RawMessage

	// RewriteMessages rewrites the Core message list.
	RewriteMessages func(ctx context.Context, req *CoreRequest)

	// InjectTools injects tool definitions into the Core request.
	InjectTools func(ctx context.Context) []CoreTool

	// MutateCoreRequest modifies the CoreRequest after conversion,
	// before sending to the upstream provider.
	MutateCoreRequest func(ctx context.Context, req *CoreRequest)

	// PostProcessCoreResponse processes the CoreResponse after conversion
	// from the upstream provider response.
	PostProcessCoreResponse func(ctx context.Context, resp *CoreResponse)

	// TransformError transforms an error message.
	TransformError func(ctx context.Context, model string, msg string) string

	// OnStreamEvent handles individual CoreStreamEvent.
	// Return true to skip/drop the event.
	OnStreamEvent func(ctx context.Context, event CoreStreamEvent) (skip bool)

	// OnStreamComplete is called when streaming completes.
	OnStreamComplete func(ctx context.Context, model string, outputText string)

	// FilterContent filters/transforms a single content block from Core responses.
	// deepseek_v4 uses this to detect and handle thinking blocks.
	FilterContent func(ctx context.Context, block *CoreContentBlock) (skip bool)

	// RememberContent remembers response content blocks for state tracking.
	// deepseek_v4 uses this to track thinking state across stream events.
	RememberContent func(ctx context.Context, content []CoreContentBlock)

	// NewStreamState initializes a new stream state object.
	// deepseek_v4 uses this to create per-stream thinking state tracking.
	NewStreamState func(ctx context.Context, model string) any

	// PrependThinkingToAssistant injects thinking content before assistant messages.
	// deepseek_v4 uses this for reasoning replay.
	PrependThinkingToAssistant func(ctx context.Context, req *CoreRequest)

	// DisablePatchProxy reports whether apply_patch proxy expansion should be
	// skipped for the given model alias. When true, apply_patch custom tools
	// are passed through as ToolRaw instead of being expanded into five
	// structured proxy tools (add_file, delete_file, update_file, replace_file, batch).
	// Populated by the codex_tool_proxy extension via the plugin registry.
	DisablePatchProxy func(model string) bool
}

// WithDefaults returns a copy of hooks with all nil function fields
// replaced by no-op implementations.
func (hooks CorePluginHooks) WithDefaults() CorePluginHooks {
	if hooks.PreprocessInput == nil {
		hooks.PreprocessInput = func(_ context.Context, _ string, raw json.RawMessage) json.RawMessage {
			return raw
		}
	}
	if hooks.RewriteMessages == nil {
		hooks.RewriteMessages = func(_ context.Context, _ *CoreRequest) {}
	}
	if hooks.InjectTools == nil {
		hooks.InjectTools = func(_ context.Context) []CoreTool { return nil }
	}
	if hooks.MutateCoreRequest == nil {
		hooks.MutateCoreRequest = func(_ context.Context, _ *CoreRequest) {}
	}
	if hooks.PostProcessCoreResponse == nil {
		hooks.PostProcessCoreResponse = func(_ context.Context, _ *CoreResponse) {}
	}
	if hooks.TransformError == nil {
		hooks.TransformError = func(_ context.Context, _ string, msg string) string { return msg }
	}
	if hooks.OnStreamEvent == nil {
		hooks.OnStreamEvent = func(_ context.Context, _ CoreStreamEvent) bool { return false }
	}
	if hooks.OnStreamComplete == nil {
		hooks.OnStreamComplete = func(_ context.Context, _ string, _ string) {}
	}
	if hooks.FilterContent == nil {
		hooks.FilterContent = func(_ context.Context, _ *CoreContentBlock) bool { return false }
	}
	if hooks.RememberContent == nil {
		hooks.RememberContent = func(_ context.Context, _ []CoreContentBlock) {}
	}
	if hooks.NewStreamState == nil {
		hooks.NewStreamState = func(_ context.Context, _ string) any { return nil }
	}
	if hooks.PrependThinkingToAssistant == nil {
		hooks.PrependThinkingToAssistant = func(_ context.Context, _ *CoreRequest) {}
	}
	if hooks.DisablePatchProxy == nil {
		hooks.DisablePatchProxy = func(_ string) bool { return false }
	}
	return hooks
}
