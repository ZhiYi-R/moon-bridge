//go:build e2e

package e2e_test

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"moonbridge/internal/config"
	"moonbridge/internal/format"
	"moonbridge/internal/protocol/anthropic"
	"moonbridge/internal/protocol/chat"
	"moonbridge/internal/protocol/google"
	"moonbridge/internal/protocol/openai"
)

// ============================================================================
// noopCacheManager — anthropic.CacheManager no-op implementation
// ============================================================================

type noopCacheManager struct{}

func (m *noopCacheManager) PlanAndInject(_ context.Context, _ *anthropic.MessageRequest, _ *format.CoreRequest) (string, string) {
	return "", ""
}

func (m *noopCacheManager) UpdateRegistry(_ context.Context, _, _ string, _ anthropic.Usage) {}

// ============================================================================
// SSE Helpers
// ============================================================================

// sseEvent writes a single SSE event to the response writer.
// If data is a string, it's written directly; otherwise it's JSON-marshalled.
func writeSSE(w http.ResponseWriter, event string, data any) {
	fmt.Fprintf(w, "event: %s\n", event)

	switch d := data.(type) {
	case string:
		fmt.Fprintf(w, "data: %s\n\n", d)
	case []byte:
		fmt.Fprintf(w, "data: %s\n\n", string(d))
	default:
		// Data that implements fmt.Stringer or has a reasonable default.
		fmt.Fprintf(w, "data: %+v\n\n", d)
	}
}

// writeSSELine writes a single line of data for a multi-line SSE event,
// without terminating the event.
func writeSSEData(w http.ResponseWriter, formatStr string, args ...any) {
	msg := fmt.Sprintf(formatStr, args...)
	for _, line := range strings.Split(msg, "\n") {
		fmt.Fprintf(w, "data: %s\n", line)
	}
}

// sseFlush flushes the response writer if it supports http.Flusher.
func sseFlush(w http.ResponseWriter) {
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// ============================================================================
// Config Factory
// ============================================================================

// e2eMinimalConfig returns a minimal config.Config suitable for E2E adapter tests.
// All 4 protocol constants have defaults; provider routing config is omitted.
func e2eMinimalConfig() config.Config {
	return config.Config{
		DefaultMaxTokens: 1024,
	}
}

// ============================================================================
// Registry Wiring Helper
// ============================================================================

// newTestRegistry creates a complete protocol adapter Registry populated with
// all four provider adapters and the OpenAI Responses client adapter.
//
// Anthropic uses a noop CacheManager. Google and Chat use nil clients
// (they are only needed for actual HTTP calls, not for request conversion).
func newTestRegistry(t testing.TB, cfg config.Config, hooks format.CorePluginHooks) *format.Registry {
	t.Helper()

	reg := format.NewRegistry()

	// --- OpenAI Responses client adapter (inbound) ---
	oaiAdapter := openai.NewOpenAIAdapter(hooks)
	if err := reg.RegisterClient(oaiAdapter); err != nil {
		t.Fatalf("RegisterClient(openai-response): %v", err)
	}
	if err := reg.RegisterClientStream(oaiAdapter); err != nil {
		t.Fatalf("RegisterClientStream(openai-response): %v", err)
	}

	// --- Anthropic provider adapter ---
	anthAdapter := anthropic.NewAnthropicProviderAdapter(cfg.DefaultMaxTokens, &noopCacheManager{}, hooks)
	if err := reg.RegisterProvider(anthAdapter); err != nil {
		t.Fatalf("RegisterProvider(anthropic): %v", err)
	}
	if err := reg.RegisterProviderStream(anthAdapter); err != nil {
		t.Fatalf("RegisterProviderStream(anthropic): %v", err)
	}

	// --- Google GenAI provider adapter ---
	geminiAdapter := google.NewGeminiProviderAdapter(cfg.DefaultMaxTokens, nil, hooks, nil, nil)
	if err := reg.RegisterProvider(geminiAdapter); err != nil {
		t.Fatalf("RegisterProvider(google-genai): %v", err)
	}
	if err := reg.RegisterProviderStream(geminiAdapter); err != nil {
		t.Fatalf("RegisterProviderStream(google-genai): %v", err)
	}

	// --- OpenAI Chat provider adapter ---
	chatAdapter := chat.NewChatProviderAdapter(cfg.DefaultMaxTokens, nil, hooks)
	if err := reg.RegisterProvider(chatAdapter); err != nil {
		t.Fatalf("RegisterProvider(openai-chat): %v", err)
	}
	if err := reg.RegisterProviderStream(chatAdapter); err != nil {
		t.Fatalf("RegisterProviderStream(openai-chat): %v", err)
	}

	return reg
}

// ============================================================================
// Environment Helpers
// ============================================================================

// loadTestEnv checks for E2E test mode and returns relevant env vars.
// In mock mode (default), returns nil. In real mode, only includes
// env vars that are actually set.

func TestMain(m *testing.M) {
	loadDotEnv(nil)
	os.Exit(m.Run())
}

func loadTestEnv(t testing.TB) map[string]string {
	t.Helper()
	loadDotEnv(t)

	env := make(map[string]string)
	candidates := []string{
		"TEST_ANTHROPIC_API_KEY",
		"TEST_OPENAI_API_KEY",
		"TEST_GEMINI_API_KEY",
	}
	for _, key := range candidates {
		if v := os.Getenv(key); v != "" {
			env[key] = v
		}
	}
	return env
}

// ============================================================================
// Assertion Helpers
// ============================================================================

// assertResponseBasics validates that an OpenAI Response has the expected
// structural fields (Object, Status, Model, OutputText).
func assertResponseBasics(t testing.TB, oaiResp *openai.Response, wantModel string) {
	t.Helper()

	if oaiResp.Object != "response" {
		t.Errorf("Object = %q, want %q", oaiResp.Object, "response")
	}
	if oaiResp.Status != "completed" && oaiResp.Status != "in_progress" {
		t.Errorf("unexpected Status = %q (want 'completed' or 'in_progress')", oaiResp.Status)
	}
	if wantModel != "" && oaiResp.Model != wantModel {
		t.Errorf("Model = %q, want %q", oaiResp.Model, wantModel)
	}
	if oaiResp.OutputText == "" {
		t.Error("OutputText is empty")
	}
}

func loadDotEnv(t testing.TB) {
	if t != nil {
		t.Helper()
	}

	dir, err := os.Getwd()
	if err != nil {
		return
	}
	for {
		path := filepath.Join(dir, ".env.test")
		if _, err := os.Stat(path); err == nil {
			f, err := os.Open(path)
			if err != nil {
				if t != nil {
					t.Logf("warning: cannot open %s: %v", path, err)
				} else {
					println("warning: cannot open", path, err.Error())
				}
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
