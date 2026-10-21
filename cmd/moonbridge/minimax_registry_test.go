package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
	"moonbridge/internal/config"
	"moonbridge/internal/protocol/chat"
)

func TestExampleConfigIncludesMiniMaxFlashFirstRegistry(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	example, err := os.ReadFile(filepath.Join(repoRoot, "config.example.yml"))
	if err != nil {
		t.Fatalf("read example config: %v", err)
	}

	var fileConfig config.FileConfig
	if err := yaml.Unmarshal(example, &fileConfig); err != nil {
		t.Fatalf("decode example config: %v", err)
	}

	for _, modelID := range []string{"MiniMax-M3.1-Flash", "MiniMax-M3"} {
		if _, ok := fileConfig.Models[modelID]; !ok {
			t.Fatalf("model %q is missing", modelID)
		}
	}

	providers := []struct {
		key      string
		protocol string
		baseURL  string
	}{
		{"minimax-global-anthropic", config.ProtocolAnthropic, "https://api.minimax.io/anthropic"},
		{"minimax-global-openai", config.ProtocolOpenAIChat, "https://api.minimax.io/v1"},
		{"minimax-cn-anthropic", config.ProtocolAnthropic, "https://api.minimaxi.com/anthropic"},
		{"minimax-cn-openai", config.ProtocolOpenAIChat, "https://api.minimaxi.com/v1"},
	}
	for _, tt := range providers {
		t.Run(tt.key, func(t *testing.T) {
			provider, ok := fileConfig.Providers[tt.key]
			if !ok {
				t.Fatalf("provider %q is missing", tt.key)
			}
			if provider.Protocol != tt.protocol {
				t.Fatalf("protocol = %q, want %q", provider.Protocol, tt.protocol)
			}
			if provider.BaseURL != tt.baseURL {
				t.Fatalf("base URL = %q, want %q", provider.BaseURL, tt.baseURL)
			}
			if len(provider.Offers) != 2 {
				t.Fatalf("offer count = %d, want 2", len(provider.Offers))
			}
			if got := provider.Offers[0].Model; got != "MiniMax-M3.1-Flash" {
				t.Fatalf("first offer = %q, want MiniMax-M3.1-Flash", got)
			}
			if got := provider.Offers[1].Model; got != "MiniMax-M3" {
				t.Fatalf("second offer = %q, want MiniMax-M3", got)
			}
			if !provider.Offers[0].Pricing.IsZero() {
				t.Fatal("Flash USD list prices must not enter the CNY billing contract")
			}
			if !provider.Offers[1].Pricing.IsZero() {
				t.Fatal("M3 must not advertise a flat price for tiered billing")
			}
		})
	}

	for _, docsRoot := range []string{
		"https://platform.minimax.io/docs",
		"https://platform.minimaxi.com/docs",
	} {
		if !contains(string(example), docsRoot) {
			t.Fatalf("example config is missing documentation root %q", docsRoot)
		}
	}

	// Exercise the config wire representation as well as decoding it.
	encoded, err := yaml.Marshal(fileConfig)
	if err != nil {
		t.Fatalf("encode example config: %v", err)
	}
	var roundTripped config.FileConfig
	if err := yaml.Unmarshal(encoded, &roundTripped); err != nil {
		t.Fatalf("decode round-tripped config: %v", err)
	}
	if !reflect.DeepEqual(fileConfig.Providers["minimax-global-openai"], roundTripped.Providers["minimax-global-openai"]) {
		t.Fatal("MiniMax OpenAI provider changed during config round-trip")
	}
}

func TestMiniMaxModelMetadata(t *testing.T) {
	tests := []struct {
		modelID       string
		contextWindow int
		modalities    []string
		reasoning     []string
	}{
		{"MiniMax-M3.1-Flash", 1000000, []string{"text", "image", "video"}, []string{"low", "medium", "high", "xhigh", "max"}},
		{"MiniMax-M3", 1000000, []string{"text", "image", "video"}, []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.modelID, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("..", "..", "metadata", "models", tt.modelID+".json"))
			if err != nil {
				t.Fatalf("read model metadata: %v", err)
			}
			var metadata config.ModelDefFileConfig
			if err := json.Unmarshal(raw, &metadata); err != nil {
				t.Fatalf("decode model metadata: %v", err)
			}
			if metadata.ContextWindow != tt.contextWindow {
				t.Fatalf("context window = %d, want %d", metadata.ContextWindow, tt.contextWindow)
			}
			if metadata.SupportsReasoning == nil || !*metadata.SupportsReasoning {
				t.Fatal("supports_reasoning = false, want true")
			}
			gotReasoning := make([]string, 0, len(metadata.SupportedReasoningLevels))
			for _, level := range metadata.SupportedReasoningLevels {
				gotReasoning = append(gotReasoning, level.Effort)
			}
			if !reflect.DeepEqual(gotReasoning, tt.reasoning) {
				t.Fatalf("supported reasoning levels = %v, want %v", gotReasoning, tt.reasoning)
			}
			if !reflect.DeepEqual(metadata.InputModalities, tt.modalities) {
				t.Fatalf("input modalities = %v, want %v", metadata.InputModalities, tt.modalities)
			}
		})
	}
}

func TestMiniMaxFlashChatHTTPSerialization(t *testing.T) {
	var request struct {
		Model    string           `json:"model"`
		Messages []map[string]any `json:"messages"`
		Stream   bool             `json:"stream"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("request path = %q, want /v1/chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer credential-free-test-key" {
			t.Fatalf("authorization = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-test","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	client := chat.NewClient(chat.ClientConfig{
		BaseURL: server.URL + "/v1",
		APIKey:  "credential-free-test-key",
		Client:  server.Client(),
	})
	response, err := client.CreateChat(t.Context(), &chat.ChatRequest{
		Model:    "MiniMax-M3.1-Flash",
		Messages: []chat.ChatMessage{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("CreateChat: %v", err)
	}
	if response.ID != "chatcmpl-test" {
		t.Fatalf("response ID = %q", response.ID)
	}
	if request.Model != "MiniMax-M3.1-Flash" || request.Stream {
		t.Fatalf("serialized request = %+v", request)
	}
	if len(request.Messages) != 1 || request.Messages[0]["role"] != "user" || request.Messages[0]["content"] != "hello" {
		t.Fatalf("serialized messages = %+v", request.Messages)
	}
}

func contains(value, needle string) bool {
	for i := 0; i+len(needle) <= len(value); i++ {
		if value[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
