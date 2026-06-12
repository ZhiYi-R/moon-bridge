package format

import (
	"context"
	"testing"
)

// ============================================================================
// Mock adapters for testing
// ============================================================================

type mockClient struct{ protocol string }

func (m *mockClient) ClientProtocol() string { return m.protocol }
func (m *mockClient) ToCoreRequest(_ context.Context, _ any) (*CoreRequest, error) {
	return &CoreRequest{}, nil
}
func (m *mockClient) FromCoreResponse(_ context.Context, _ *CoreResponse) (any, error) {
	return nil, nil
}

type mockProvider struct{ protocol string }

func (m *mockProvider) ProviderProtocol() string { return m.protocol }
func (m *mockProvider) FromCoreRequest(_ context.Context, _ *CoreRequest) (any, error) {
	return nil, nil
}
func (m *mockProvider) ToCoreResponse(_ context.Context, _ any) (*CoreResponse, error) {
	return &CoreResponse{}, nil
}

type mockClientStream struct{ protocol string }

func (m *mockClientStream) ClientProtocol() string { return m.protocol }
func (m *mockClientStream) FromCoreStream(_ context.Context, _ *CoreRequest, _ <-chan CoreStreamEvent) (any, error) {
	return nil, nil
}

type mockProviderStream struct{ protocol string }

func (m *mockProviderStream) ProviderProtocol() string { return m.protocol }
func (m *mockProviderStream) ToCoreStream(_ context.Context, _ any) (<-chan CoreStreamEvent, error) {
	return nil, nil
}

// ============================================================================
// Tests
// ============================================================================

func TestRegistry_RegisterAndGetClient(t *testing.T) {
	r := NewRegistry()
	m := &mockClient{protocol: "test-proto"}

	if err := r.RegisterClient(m); err != nil {
		t.Fatalf("RegisterClient() error = %v", err)
	}

	got, ok := r.GetClient("test-proto")
	if !ok {
		t.Fatal("GetClient() returned false for registered adapter")
	}
	if got != m {
		t.Fatal("GetClient() returned unexpected adapter")
	}
}

func TestRegistry_RegisterAndGetProvider(t *testing.T) {
	r := NewRegistry()
	m := &mockProvider{protocol: "anthropic"}

	if err := r.RegisterProvider(m); err != nil {
		t.Fatalf("RegisterProvider() error = %v", err)
	}

	got, ok := r.GetProvider("anthropic")
	if !ok {
		t.Fatal("GetProvider() returned false for registered adapter")
	}
	if got != m {
		t.Fatal("GetProvider() returned unexpected adapter")
	}
}

func TestRegistry_RegisterAndGetClientStream(t *testing.T) {
	r := NewRegistry()
	m := &mockClientStream{protocol: "stream-proto"}

	if err := r.RegisterClientStream(m); err != nil {
		t.Fatalf("RegisterClientStream() error = %v", err)
	}

	got, ok := r.GetClientStream("stream-proto")
	if !ok {
		t.Fatal("GetClientStream() returned false for registered adapter")
	}
	if got != m {
		t.Fatal("GetClientStream() returned unexpected adapter")
	}
}

func TestRegistry_RegisterAndGetProviderStream(t *testing.T) {
	r := NewRegistry()
	m := &mockProviderStream{protocol: "anthropic-stream"}

	if err := r.RegisterProviderStream(m); err != nil {
		t.Fatalf("RegisterProviderStream() error = %v", err)
	}

	got, ok := r.GetProviderStream("anthropic-stream")
	if !ok {
		t.Fatal("GetProviderStream() returned false for registered adapter")
	}
	if got != m {
		t.Fatal("GetProviderStream() returned unexpected adapter")
	}
}

func TestRegistry_DuplicateClient(t *testing.T) {
	r := NewRegistry()
	m1 := &mockClient{protocol: "dup"}
	m2 := &mockClient{protocol: "dup"}

	if err := r.RegisterClient(m1); err != nil {
		t.Fatalf("first RegisterClient() error = %v", err)
	}
	if err := r.RegisterClient(m2); err == nil {
		t.Fatal("expected error for duplicate client registration")
	}
}

func TestRegistry_DuplicateProvider(t *testing.T) {
	r := NewRegistry()
	m1 := &mockProvider{protocol: "dup"}
	m2 := &mockProvider{protocol: "dup"}

	if err := r.RegisterProvider(m1); err != nil {
		t.Fatalf("first RegisterProvider() error = %v", err)
	}
	if err := r.RegisterProvider(m2); err == nil {
		t.Fatal("expected error for duplicate provider registration")
	}
}

func TestRegistry_DuplicateClientStream(t *testing.T) {
	r := NewRegistry()
	m1 := &mockClientStream{protocol: "dup-stream"}
	m2 := &mockClientStream{protocol: "dup-stream"}

	if err := r.RegisterClientStream(m1); err != nil {
		t.Fatalf("first RegisterClientStream() error = %v", err)
	}
	if err := r.RegisterClientStream(m2); err == nil {
		t.Fatal("expected error for duplicate client stream registration")
	}
}

func TestRegistry_DuplicateProviderStream(t *testing.T) {
	r := NewRegistry()
	m1 := &mockProviderStream{protocol: "dup-stream"}
	m2 := &mockProviderStream{protocol: "dup-stream"}

	if err := r.RegisterProviderStream(m1); err != nil {
		t.Fatalf("first RegisterProviderStream() error = %v", err)
	}
	if err := r.RegisterProviderStream(m2); err == nil {
		t.Fatal("expected error for duplicate provider stream registration")
	}
}

func TestRegistry_GetClientNotFound(t *testing.T) {
	r := NewRegistry()
	_, ok := r.GetClient("nonexistent")
	if ok {
		t.Fatal("GetClient() returned true for unregistered protocol")
	}
}

func TestRegistry_GetProviderNotFound(t *testing.T) {
	r := NewRegistry()
	_, ok := r.GetProvider("nonexistent")
	if ok {
		t.Fatal("GetProvider() returned true for unregistered protocol")
	}
}

func TestRegistry_GetClientStreamNotFound(t *testing.T) {
	r := NewRegistry()
	_, ok := r.GetClientStream("nonexistent")
	if ok {
		t.Fatal("GetClientStream() returned true for unregistered protocol")
	}
}

func TestRegistry_GetProviderStreamNotFound(t *testing.T) {
	r := NewRegistry()
	_, ok := r.GetProviderStream("nonexistent")
	if ok {
		t.Fatal("GetProviderStream() returned true for unregistered protocol")
	}
}

func TestRegistry_ProviderProtocols(t *testing.T) {
	r := NewRegistry()

	r.RegisterProvider(&mockProvider{protocol: "anthropic"})
	r.RegisterProvider(&mockProvider{protocol: "gemini"})

	protocols := r.ProviderProtocols()
	if len(protocols) != 2 {
		t.Fatalf("ProviderProtocols() = %v, want 2 entries", protocols)
	}

	m := make(map[string]bool)
	for _, p := range protocols {
		m[p] = true
	}
	if !m["anthropic"] || !m["gemini"] {
		t.Fatalf("ProviderProtocols() missing expected entries: %v", protocols)
	}
}

func TestRegistry_ClientProtocols(t *testing.T) {
	r := NewRegistry()

	r.RegisterClient(&mockClient{protocol: "openai-response"})
	r.RegisterClient(&mockClient{protocol: "test-proto"})

	protocols := r.ClientProtocols()
	if len(protocols) != 2 {
		t.Fatalf("ClientProtocols() = %v, want 2 entries", protocols)
	}

	m := make(map[string]bool)
	for _, p := range protocols {
		m[p] = true
	}
	if !m["openai-response"] || !m["test-proto"] {
		t.Fatalf("ClientProtocols() missing expected entries: %v", protocols)
	}
}

func TestRegistry_EmptyProtocols(t *testing.T) {
	r := NewRegistry()

	if protocols := r.ProviderProtocols(); len(protocols) != 0 {
		t.Fatalf("ProviderProtocols() = %v, want empty", protocols)
	}
	if protocols := r.ClientProtocols(); len(protocols) != 0 {
		t.Fatalf("ClientProtocols() = %v, want empty", protocols)
	}
}
