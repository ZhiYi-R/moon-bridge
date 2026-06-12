//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	visualpkg "moonbridge/internal/extension/visual"
	"moonbridge/internal/format"
	"moonbridge/internal/protocol/chat"
)

// TestVisualOnOpenAIChat_OrchestratesBriefAcrossTwoMocks proves the visual
// orchestrator works end-to-end on the openai-chat protocol:
//
//  1. The upstream (text-only) model is asked to describe an image and chooses
//     to call the visual_brief tool with image_refs ["Image #1"].
//  2. The orchestrator strips the image from the request before forwarding it
//     to the upstream and routes the actual image to the configured visual
//     provider on the openai-chat protocol.
//  3. The brief text returned by the visual provider is fed back to the
//     upstream as a tool_result, the upstream emits a final answer, and the
//     orchestrator returns it.
//
// Asserting the upstream never sees image_url content guards against the
// previous bug where chat-protocol upstreams received the raw base64 payload
// because the orchestrator was not wired in.
func TestVisualOnOpenAIChat_OrchestratesBriefAcrossTwoMocks(t *testing.T) {
	ctx := context.Background()

	type observed struct {
		mu     sync.Mutex
		bodies [][]byte
		rounds int
	}
	upstreamObs := &observed{}
	visualObs := &observed{}

	// --- Upstream (text-only "deepseek") mock: returns visual_brief tool_use
	// on the first call, final text on the second.
	upstreamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		upstreamObs.mu.Lock()
		upstreamObs.rounds++
		round := upstreamObs.rounds
		upstreamObs.bodies = append(upstreamObs.bodies, body)
		upstreamObs.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if round == 1 {
			fmt.Fprint(w, `{
				"id":"chatcmpl_upstream_1","object":"chat.completion","model":"deepseek-test",
				"choices":[{"index":0,"finish_reason":"tool_calls","message":{
					"role":"assistant","content":null,
					"tool_calls":[{
						"id":"call_visual_1","type":"function",
						"function":{"name":"visual_brief","arguments":"{\"image_refs\":[\"Image #1\"],\"context\":\"describe\"}"}
					}]
				}}],
				"usage":{"prompt_tokens":50,"completion_tokens":10,"total_tokens":60}
			}`)
			return
		}
		fmt.Fprint(w, `{
			"id":"chatcmpl_upstream_2","object":"chat.completion","model":"deepseek-test",
			"choices":[{"index":0,"finish_reason":"stop","message":{
				"role":"assistant","content":"based on the brief: a chat screenshot"
			}}],
			"usage":{"prompt_tokens":80,"completion_tokens":12,"total_tokens":92}
		}`)
	}))
	defer upstreamSrv.Close()

	// --- Visual provider (vision-capable "qwen-vl") mock: returns a brief.
	visualSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		visualObs.mu.Lock()
		visualObs.rounds++
		visualObs.bodies = append(visualObs.bodies, body)
		visualObs.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{
			"id":"chatcmpl_visual_1","object":"chat.completion","model":"qwen-vl-test",
			"choices":[{"index":0,"finish_reason":"stop","message":{
				"role":"assistant","content":"a chat screenshot with two people"
			}}],
			"usage":{"prompt_tokens":200,"completion_tokens":10,"total_tokens":210}
		}`)
	}))
	defer visualSrv.Close()

	upstreamChatClient := chat.NewClient(chat.ClientConfig{
		BaseURL: upstreamSrv.URL, APIKey: "k", Client: upstreamSrv.Client(),
	})
	visualChatClient := chat.NewClient(chat.ClientConfig{
		BaseURL: visualSrv.URL, APIKey: "k", Client: visualSrv.Client(),
	})

	hooks := format.CorePluginHooks{}.WithDefaults()
	upstreamAdapter := chat.NewChatProviderAdapter(2048, nil, hooks)
	visualAdapter := chat.NewChatProviderAdapter(2048, nil, hooks)

	// Build a CoreProvider that drives the chat adapter end-to-end against a
	// chat.Client — equivalent to the production adapterCoreProvider wired
	// through chatProviderClient.
	chatCoreProvider := func(adapter *chat.ChatProviderAdapter, client *chat.Client) visualpkg.CoreProvider {
		return visualpkg.CoreProviderFunc(func(ctx context.Context, req *format.CoreRequest) (*format.CoreResponse, error) {
			upstreamAny, err := adapter.FromCoreRequest(ctx, req)
			if err != nil {
				return nil, err
			}
			chatReq := upstreamAny.(*chat.ChatRequest)
			chatResp, err := client.CreateChat(ctx, chatReq)
			if err != nil {
				return nil, err
			}
			return adapter.ToCoreResponse(ctx, chatResp)
		})
	}

	bridge := visualpkg.NewCoreBridge(
		chatCoreProvider(upstreamAdapter, upstreamChatClient),
		chatCoreProvider(visualAdapter, visualChatClient),
		"qwen-vl-test", 4, 2048,
	)

	coreReq := &format.CoreRequest{
		Model: "deepseek-test",
		Messages: []format.CoreMessage{{
			Role: "user",
			Content: []format.CoreContentBlock{
				{Type: "text", Text: "describe the image"},
				{Type: "image", ImageData: "ZHVtbXlpbWFnZQ==", MediaType: "image/png"},
			},
		}},
		ToolChoice: &format.CoreToolChoice{Mode: "auto"},
	}

	resp, err := bridge.CreateCore(ctx, coreReq)
	if err != nil {
		t.Fatalf("CreateCore error: %v", err)
	}

	// --- Assertion 1: upstream was called twice (tool_use round + final answer).
	if upstreamObs.rounds != 2 {
		t.Fatalf("upstream rounds = %d, want 2 (visual_brief tool_use + final)", upstreamObs.rounds)
	}
	// --- Assertion 2: visual provider was called once (executing visual_brief).
	if visualObs.rounds != 1 {
		t.Fatalf("visual rounds = %d, want 1", visualObs.rounds)
	}

	// --- Assertion 3: the upstream NEVER receives image_url content. This is
	// the core regression guard for the wiring bug — before the fix, the
	// upstream would have received the full base64 image_url payload.
	for i, body := range upstreamObs.bodies {
		if strings.Contains(string(body), `"image_url"`) {
			t.Fatalf("upstream round %d body contains image_url payload (orchestrator did not strip): %s", i+1, string(body))
		}
		if strings.Contains(string(body), `"ZHVtbXlpbWFnZQ=="`) {
			t.Fatalf("upstream round %d body contains raw base64 image data: %s", i+1, string(body))
		}
	}

	// --- Assertion 4: the visual provider DOES receive the image, with the
	// reconstructed data: URL (the chat adapter must rebuild the data URL
	// from raw base64 + media type).
	if visualObs.rounds < 1 {
		t.Fatal("visual provider was not called at all")
	}
	visualBody := visualObs.bodies[0]
	if !strings.Contains(string(visualBody), `"image_url"`) {
		t.Fatalf("visual body missing image_url part: %s", string(visualBody))
	}
	if !strings.Contains(string(visualBody), "data:image/png;base64,ZHVtbXlpbWFnZQ==") {
		t.Fatalf("visual body missing reconstructed data URL: %s", string(visualBody))
	}

	// --- Assertion 5: the orchestrator returned the upstream's final answer.
	if len(resp.Messages) == 0 || len(resp.Messages[0].Content) == 0 {
		t.Fatal("orchestrator returned empty response")
	}
	finalText := ""
	for _, block := range resp.Messages[0].Content {
		if block.Type == "text" {
			finalText = block.Text
			break
		}
	}
	if !strings.Contains(finalText, "chat screenshot") {
		t.Fatalf("final response = %q, want it to incorporate the visual brief", finalText)
	}
}
