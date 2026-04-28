package provider

import "testing"

func TestProviderManagerRoutesProtocolAndUpstreamModel(t *testing.T) {
	manager, err := NewProviderManager(map[string]ProviderConfig{
		"default": {
			BaseURL: "https://anthropic.example.test",
			APIKey:  "anthropic-key",
		},
		"openai": {
			BaseURL:  "https://openai.example.test",
			APIKey:   "openai-key",
			Protocol: "openai-response",
		},
	}, map[string]ModelRoute{
		"image": {Provider: "openai", Name: "gpt-image-1.5"},
	})
	if err != nil {
		t.Fatalf("NewProviderManager() error = %v", err)
	}

	if got := manager.ProtocolForModel("image"); got != "openai-response" {
		t.Fatalf("ProtocolForModel(image) = %q", got)
	}
	if got := manager.UpstreamModelFor("image"); got != "gpt-image-1.5" {
		t.Fatalf("UpstreamModelFor(image) = %q", got)
	}
	if got := manager.ProtocolForModel("unrouted"); got != "anthropic" {
		t.Fatalf("ProtocolForModel(unrouted) = %q", got)
	}
	if got := manager.UpstreamModelFor("unrouted"); got != "unrouted" {
		t.Fatalf("UpstreamModelFor(unrouted) = %q", got)
	}
}

func TestProviderManagerUsesDefaultProtocolForUnroutedModels(t *testing.T) {
	manager, err := NewProviderManager(map[string]ProviderConfig{
		"default": {
			BaseURL:  "https://openai.example.test",
			APIKey:   "openai-key",
			Protocol: "openai-response",
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewProviderManager() error = %v", err)
	}

	if got := manager.ProtocolForModel("gpt-test"); got != "openai-response" {
		t.Fatalf("ProtocolForModel(unrouted default openai) = %q", got)
	}
}
