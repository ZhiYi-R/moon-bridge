package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"moonbridge/internal/config"
	"moonbridge/internal/extension/plugin"
	"moonbridge/internal/format"
	"moonbridge/internal/protocol/anthropic"
	"moonbridge/internal/protocol/cache"
	"moonbridge/internal/protocol/chat"
	"moonbridge/internal/protocol/openai"
	"moonbridge/internal/service/provider"
	"moonbridge/internal/service/runtime"
	"moonbridge/internal/service/stats"
)

// Exercise the example YAML through the production loader, provider manager,
// Responses decoder, outbound adapter and real HTTP/SSE clients. Every provider
// endpoint points to a local server; no credential or paid inference is used.
func TestMiniMaxFlashConfiguredHTTPAndSSE(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, tt := range []struct {
			name, model, reasoning, provider, wireModel, effort, thinking string
		}{
			{"alias-default", "minimax", "", "minimax-global-openai", "MiniMax-M3.1-Flash", "max", "adaptive"},
			{"bare-flash", "MiniMax-M3.1-Flash", `"effort":"high"`, "minimax-global-openai", "MiniMax-M3.1-Flash", "high", "adaptive"},
			{"explicit-china-chat", "minimax-cn-openai/MiniMax-M3.1-Flash", `"effort":"low"`, "minimax-cn-openai", "MiniMax-M3.1-Flash", "low", "adaptive"},
			{"explicit-anthropic", "minimax-global-anthropic/MiniMax-M3.1-Flash", `"effort":"xhigh"`, "minimax-global-anthropic", "MiniMax-M3.1-Flash", "xhigh", "adaptive"},
			{"explicit-china-anthropic", "minimax-cn-anthropic/MiniMax-M3.1-Flash", `"effort":"max"`, "minimax-cn-anthropic", "MiniMax-M3.1-Flash", "max", "adaptive"},
			{"m3-chat", "MiniMax-M3", `"type":"disabled"`, "minimax-global-openai", "MiniMax-M3", "", "disabled"},
			{"m3-anthropic", "minimax-global-anthropic/MiniMax-M3", `"type":"adaptive"`, "minimax-global-anthropic", "MiniMax-M3", "", "adaptive"},
			{"legacy-explicit", "minimax-global-openai/MiniMax-M2.7", "", "minimax-global-openai", "MiniMax-M2.7", "", ""},
			{"custom-saved-route", "saved", "", "minimax-cn-openai", "custom-model", "", ""},
			{"non-minimax", "anthropic/claude-sonnet-4-20250514", "", "anthropic", "claude-sonnet-4-20250514", "", ""},
			{"invalid-flash-effort", "minimax", `"effort":"invalid"`, "", "", "", ""},
			{"invalid-flash-thinking", "minimax", `"type":"disabled"`, "", "", "", ""},
			{"invalid-anthropic-effort", "minimax-global-anthropic/MiniMax-M3.1-Flash", `"effort":"invalid"`, "", "", "", ""},
			{"invalid-m3-thinking", "MiniMax-M3", `"type":"enabled"`, "", "", "", ""},
			{"m3-ignores-effort-chat", "MiniMax-M3", `"effort":"high"`, "minimax-global-openai", "MiniMax-M3", "", "adaptive"},
			{"m3-ignores-effort-anthropic", "minimax-global-anthropic/MiniMax-M3", `"effort":"high","type":"disabled"`, "minimax-global-anthropic", "MiniMax-M3", "", "disabled"},
			{"invalid-numeric-effort", "minimax", `"effort":42`, "", "", "", ""},
			{"invalid-numeric-type", "minimax-global-anthropic/MiniMax-M3", `"type":42`, "", "", "", ""},
		} {
			t.Run(fmt.Sprintf("%s/stream=%t", tt.name, stream), func(t *testing.T) {
				var got map[string]any
				var gotPath string
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					gotPath = r.URL.Path
					if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
						t.Error(err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					isChat := strings.HasSuffix(r.URL.Path, "/chat/completions")
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						if isChat {
							fmt.Fprint(w, "data: {\"id\":\"local\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"}}]}\n\ndata: {\"id\":\"local\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
						} else {
							fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"local\",\"type\":\"message\",\"role\":\"assistant\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
						}
					} else {
						w.Header().Set("Content-Type", "application/json")
						if isChat {
							fmt.Fprint(w, `{"id":"local","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
						} else {
							fmt.Fprint(w, `{"id":"local","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
						}
					}
				}))
				defer upstream.Close()

				data, err := os.ReadFile("../../../config.example.yml")
				if err != nil {
					t.Fatal(err)
				}
				var fc config.FileConfig
				if err := yaml.Unmarshal(data, &fc); err != nil {
					t.Fatal(err)
				}
				// Disable optional orchestration extensions, preserving the example
				// model/provider/route graph under test. Default selection is opt-in.
				fc.Extensions = nil
				for key, def := range fc.Models {
					def.Extensions = nil
					fc.Models[key] = def
				}
				fc.Defaults.Model = "minimax"
				// The existing Kimi example references a commented-out model.
				delete(fc.Providers, "kimi")
				fc.Models["custom-model"] = config.ModelDefFileConfig{}
				fc.Routes["saved"] = config.RouteFileConfig{Model: "custom-model", Provider: "minimax-cn-openai"}
				for key, def := range fc.Providers {
					suffix := "/anthropic"
					if def.Protocol == config.ProtocolOpenAIChat {
						suffix = "/v1"
					}
					def.BaseURL = upstream.URL + "/" + key + suffix
					def.APIKey = "credential-free-test-key"
					fc.Providers[key] = def
				}
				encoded, err := yaml.Marshal(fc)
				if err != nil {
					t.Fatal(err)
				}
				cfg, err := config.LoadFromYAML(encoded)
				if err != nil {
					t.Fatal(err)
				}
				providerCfg := provider.BuildProviderDefsFromConfig(config.ProviderFromGlobalConfig(&cfg))
				routes := provider.BuildModelRoutesFromConfig(config.ProviderFromGlobalConfig(&cfg))
				pm, err := provider.NewProviderManager(providerCfg, routes)
				if err != nil {
					t.Fatal(err)
				}
				for key := range cfg.ProviderDefs {
					if strings.HasPrefix(key, "minimax-") && pm.FirstUpstreamModelForKey(key) != "MiniMax-M3.1-Flash" && key != "minimax-cn-openai" {
						t.Fatalf("unstable first model for %s", key)
					}
				}
				registry := format.NewRegistry()
				inbound := openai.NewOpenAIAdapter(format.CorePluginHooks{})
				chatAdapter := chat.NewChatProviderAdapter(cfg.DefaultMaxTokens, nil, format.CorePluginHooks{})
				anthropicAdapter := anthropic.NewAnthropicProviderAdapter(cfg.DefaultMaxTokens, anthropic.NewCacheManager(&cfg.Cache, cache.NewMemoryRegistry()), format.CorePluginHooks{})
				for _, err := range []error{registry.RegisterClient(inbound), registry.RegisterClientStream(inbound), registry.RegisterProvider(chatAdapter), registry.RegisterProviderStream(chatAdapter), registry.RegisterProvider(anthropicAdapter), registry.RegisterProviderStream(anthropicAdapter)} {
					if err != nil {
						t.Fatal(err)
					}
				}
				rt := runtime.NewRuntime(cfg, pm, provider.BuildPricingFromConfig(config.ProviderFromGlobalConfig(&cfg)))
				sc := config.ServerFromGlobalConfig(&cfg)
				sessionStats := stats.NewSessionStats()
				sessionStats.SetPricing(provider.BuildPricingFromConfig(config.ProviderFromGlobalConfig(&cfg)))
				s := New(Config{ServerCfg: sc, AppConfig: sc, ProviderMgr: pm, Runtime: rt, AdapterRegistry: registry, Stats: sessionStats, PluginRegistry: plugin.NewRegistry(nil)})
				body := map[string]any{"input": "hello", "stream": stream}
				if tt.model != "" {
					body["model"] = tt.model
				}
				if tt.reasoning != "" {
					var reasoning map[string]any
					if err := json.Unmarshal([]byte("{"+tt.reasoning+"}"), &reasoning); err != nil {
						t.Fatal(err)
					}
					body["reasoning"] = reasoning
				}
				raw, err := json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(raw)))
				response := httptest.NewRecorder()
				s.handleResponses(response, request)
				if strings.HasPrefix(tt.name, "invalid-") {
					if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"type":"invalid_request_error"`) || !strings.Contains(response.Body.String(), `"code":"invalid_reasoning"`) {
						t.Fatalf("invalid input response %d: %s", response.Code, response.Body.String())
					}
					if got != nil || gotPath != "" {
						t.Fatalf("invalid reasoning reached upstream: %s %v", gotPath, got)
					}
					return
				}
				if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "ok") {
					t.Fatalf("response %d: %s", response.Code, response.Body.String())
				}
				if !stream && (tt.wireModel == "MiniMax-M3" || tt.wireModel == "MiniMax-M3.1-Flash") {
					summary := sessionStats.Summary()
					if summary.Requests != 1 || summary.UnpricedRequests != 1 || summary.CostValue() != nil {
						t.Fatalf("real server request did not retain unknown pricing: %+v", summary)
					}
				}
				def := cfg.ProviderDefs[tt.provider]
				wantPath := "/" + tt.provider + "/anthropic/v1/messages"
				if def.Protocol == config.ProtocolOpenAIChat {
					wantPath = "/" + tt.provider + "/v1/chat/completions"
				}
				wireStream, _ := got["stream"].(bool)
				if gotPath != wantPath || got["model"] != tt.wireModel || wireStream != stream {
					t.Fatalf("path=%s payload=%v", gotPath, got)
				}
				thinking, _ := got["thinking"].(map[string]any)
				if tt.thinking == "" {
					if thinking != nil {
						t.Fatalf("unexpected thinking: %v", thinking)
					}
				} else if thinking["type"] != tt.thinking {
					t.Fatalf("thinking=%v", thinking)
				}
				effort, _ := got["reasoning_effort"].(string)
				if def.Protocol != config.ProtocolOpenAIChat {
					output, _ := got["output_config"].(map[string]any)
					effort, _ = output["effort"].(string)
				}
				if effort != tt.effort {
					t.Fatalf("effort=%q want=%q payload=%v", effort, tt.effort, got)
				}
			})
		}
	}
}
