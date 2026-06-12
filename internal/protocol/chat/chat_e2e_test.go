//go:build e2e

package chat_test

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"moonbridge/internal/protocol/chat"
)

// loadDotEnv reads key=value lines from .env.test relative to the project root
// (walked up from the test file's working directory) and sets them as OS env vars.
// Existing env vars are not overwritten — OS-level values take precedence.
func loadDotEnv(t *testing.T) {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		return
	}
	for {
		path := filepath.Join(dir, ".env.test")
		if _, err := os.Stat(path); err == nil {
			f, err := os.Open(path)
			if err != nil {
				t.Logf("warning: cannot open %s: %v", path, err)
				return
			}
			defer f.Close()

			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				parts := strings.SplitN(line, "=", 2)
				if len(parts) != 2 {
					continue
				}
				key := strings.TrimSpace(parts[0])
				val := strings.TrimSpace(parts[1])
				// Remove optional surrounding quotes
				val = strings.Trim(val, `"'`)
				if key != "" && os.Getenv(key) == "" {
					os.Setenv(key, val)
				}
			}
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Log(".env.test not found — relying on OS env vars")
			return
		}
		dir = parent
	}
}

func TestE2EChatProvider(t *testing.T) {
	loadDotEnv(t)

	apiKey := os.Getenv("TEST_OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("set TEST_OPENAI_API_KEY (via .env.test or OS env)")
	}

	model := os.Getenv("TEST_OPENAI_MODEL")
	if model == "" {
		model = "gpt-4o-mini"
	}
	baseURL := os.Getenv("TEST_OPENAI_BASE_URL")

	client := chat.NewClient(chat.ClientConfig{
		APIKey:  apiKey,
		BaseURL: baseURL,
	})

	t.Run("text", func(t *testing.T) {
		req := &chat.ChatRequest{
			Model: model,
			Messages: []chat.ChatMessage{
				{Role: "user", Content: "Say hello in one word"},
			},
			MaxTokens: 10,
		}

		resp, err := client.CreateChat(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if len(resp.Choices) == 0 {
			t.Fatal("no choices returned")
		}
		content := resp.Choices[0].Message.Content
		t.Logf("Model: %s, Response: %s", model, content)
		if content == "" || content == nil {
			t.Error("empty response content")
		}
	})

	t.Run("tool_call", func(t *testing.T) {
		req := &chat.ChatRequest{
			Model: model,
			Messages: []chat.ChatMessage{
				{Role: "user", Content: "What is the weather in Paris? Use the get_weather tool to find out."},
			},
			Tools: []chat.ChatTool{
				{
					Type: "function",
					Function: chat.FunctionDef{
						Name:        "get_weather",
						Description: "Get the current weather for a city",
						Parameters: map[string]any{
							"type": "object",
							"properties": map[string]any{
								"location": map[string]any{
									"type":        "string",
									"description": "City name, e.g. Paris, France",
								},
							},
							"required": []any{"location"},
						},
					},
				},
			},
			ToolChoice: []byte(`"auto"`),
			MaxTokens:  200,
		}

		resp, err := client.CreateChat(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if len(resp.Choices) == 0 {
			t.Fatal("no choices returned")
		}

		msg := resp.Choices[0].Message
		if len(msg.ToolCalls) == 0 {
			t.Fatalf("expected tool_call, got content=%v finish_reason=%s", msg.Content, resp.Choices[0].FinishReason)
		}
		tc := msg.ToolCalls[0]
		t.Logf("ToolCall: %s(%s) -> %s", tc.Function.Name, tc.ID, tc.Function.Arguments)

		if tc.Type != "function" {
			t.Errorf("expected type function, got %s", tc.Type)
		}
		if tc.Function.Name != "get_weather" {
			t.Errorf("expected function get_weather, got %s", tc.Function.Name)
		}
		if tc.Function.Arguments == nil || string(tc.Function.Arguments) == "" {
			t.Error("empty tool call arguments")
		}
	})
}
