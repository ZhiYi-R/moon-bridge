//go:build e2e

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"moonbridge/internal/foundation/config"
	"moonbridge/internal/protocol/anthropic"
	"moonbridge/internal/protocol/bridge"
	"moonbridge/internal/protocol/cache"
	"moonbridge/internal/service/server"
)

func TestResponsesTextE2E(t *testing.T) {
	e2eConfig := loadE2EConfig(t)
	handler := newE2EHandler(e2eConfig.Config)

	response := postResponses(t, handler, map[string]any{
		"model":             e2eConfig.ModelAlias,
		"instructions":      "Reply briefly. Do not use Markdown.",
		"input":             "Reply with the words Moon Bridge e2e ok.",
		"max_output_tokens": 64,
	})

	if response["object"] != "response" {
		t.Fatalf("object = %v", response["object"])
	}
	if response["status"] == "failed" {
		t.Fatalf("response failed: %+v", response)
	}
	outputText, _ := response["output_text"].(string)
	if strings.TrimSpace(outputText) == "" {
		t.Fatalf("output_text is empty: %+v", response)
	}
}

func TestResponsesFunctionToolE2E(t *testing.T) {
	e2eConfig := loadE2EConfig(t)
	handler := newE2EHandler(e2eConfig.Config)

	response := postResponses(t, handler, map[string]any{
		"model": e2eConfig.ModelAlias,
		"input": "Use the lookup_weather tool for Paris. Do not answer directly.",
		"tools": []map[string]any{
			{
				"type":        "function",
				"name":        "lookup_weather",
				"description": "Look up weather for a city.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"city": map[string]any{"type": "string"},
					},
					"required": []string{"city"},
				},
			},
		},
		"tool_choice":       "required",
		"max_output_tokens": 128,
	})

	call := findFunctionCall(t, response, "lookup_weather")
	if strings.TrimSpace(call["call_id"].(string)) == "" {
		t.Fatalf("call_id is empty: %+v", call)
	}
	var arguments map[string]any
	if err := json.Unmarshal([]byte(call["arguments"].(string)), &arguments); err != nil {
		t.Fatalf("arguments are not JSON: %v; call=%+v", err, call)
	}
	if strings.TrimSpace(arguments["city"].(string)) == "" {
		t.Fatalf("city argument is empty: %+v", arguments)
	}
}

func TestResponsesPromptCacheE2E(t *testing.T) {
	e2eConfig := loadE2EConfig(t)
	if os.Getenv("MOONBRIDGE_E2E_CACHE") != "1" {
		t.Skip("set MOONBRIDGE_E2E_CACHE=1 to run cache-costing e2e")
	}
	handler := newE2EHandlerWithCache(e2eConfig.Config, config.CacheConfig{
		Mode:                     "explicit",
		TTL:                      "5m",
		PromptCaching:            true,
		AutomaticPromptCache:     true,
		ExplicitCacheBreakpoints: true,
		MaxBreakpoints:           4,
		MinCacheTokens:           1,
		ExpectedReuse:            2,
		MinimumValueScore:        1,
	})

	longContext := strings.Repeat("Moon Bridge cache prefix stability sentence. ", 900)
	request := map[string]any{
		"model":             e2eConfig.ModelAlias,
		"instructions":      longContext,
		"input":             "Answer with one short sentence.",
		"prompt_cache_key":  "moonbridge-e2e-cache",
		"max_output_tokens": 32,
	}

	first := postResponses(t, handler, request)
	second := postResponses(t, handler, request)

	if cachedTokens(second) == 0 && cacheCreationTokens(first) == 0 {
		t.Fatalf("no cache usage signals observed; first=%+v second=%+v", first["usage"], second["usage"])
	}
}

type e2eConfig struct {
	Config     config.Config
	ModelAlias string
}

func newE2EHandler(cfg config.Config) http.Handler {
	return newE2EHandlerWithCache(cfg, config.CacheConfig{Mode: "off"})
}

func newE2EHandlerWithCache(cfg config.Config, cacheConfig config.CacheConfig) http.Handler {
	cfg.Cache = cacheConfig
	return server.New(server.Config{
		Bridge: bridge.New(cfg, cache.NewMemoryRegistry(), bridge.PluginHooks{}),
		Provider: anthropic.NewClient(anthropic.ClientConfig{
			BaseURL:   cfg.ProviderBaseURL,
			APIKey:    cfg.ProviderAPIKey,
			Version:   cfg.ProviderVersion,
			UserAgent: cfg.ProviderUserAgent,
		}),
	})
}

func postResponses(t *testing.T, handler http.Handler, payload map[string]any) map[string]any {
	t.Helper()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal payload error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/v1/responses", bytes.NewReader(body))
	request.Header.Set("content-type", "application/json")
	handler.ServeHTTP(recorder, request)

	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("response is not JSON: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, response = %+v", recorder.Code, response)
	}
	return response
}

func findFunctionCall(t *testing.T, response map[string]any, name string) map[string]any {
	t.Helper()

	output, ok := response["output"].([]any)
	if !ok {
		t.Fatalf("output missing or invalid: %+v", response)
	}
	for _, item := range output {
		itemMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if itemMap["type"] == "function_call" && itemMap["name"] == name {
			return itemMap
		}
	}
	t.Fatalf("function_call %q not found in output: %+v", name, output)
	return nil
}

func cachedTokens(response map[string]any) int {
	usage, _ := response["usage"].(map[string]any)
	details, _ := usage["input_tokens_details"].(map[string]any)
	return numberAsInt(details["cached_tokens"])
}

func cacheCreationTokens(response map[string]any) int {
	metadata, _ := response["metadata"].(map[string]any)
	providerUsage, _ := metadata["provider_usage"].(map[string]any)
	return numberAsInt(providerUsage["cache_creation_input_tokens"])
}

func loadE2EConfig(t *testing.T) e2eConfig {
	t.Helper()

	configPath := os.Getenv("MOONBRIDGE_CONFIG")
	if configPath == "" {
		configPath = filepath.Join(findProjectRoot(t), config.DefaultConfigFileName)
	}

	cfg, err := config.LoadFromFile(configPath)
	if errors.Is(err, os.ErrNotExist) {
		t.Skipf("config file %s not found", configPath)
	}
	if err != nil {
		t.Fatalf("load e2e config %s error = %v", configPath, err)
	}

	modelAlias, err := e2eModelAlias(cfg.Routes)
	if err != nil {
		t.Fatal(err)
	}
	return e2eConfig{Config: cfg, ModelAlias: modelAlias}
}

func e2eModelAlias(routes map[string]config.RouteEntry) (string, error) {
	if route, ok := routes["e2e-model"]; ok && route.Model != "" {
		return "e2e-model", nil
	}
	if route, ok := routes["moonbridge"]; ok && route.Model != "" {
		return "moonbridge", nil
	}

	aliases := make([]string, 0, len(models))
	for alias, mapped := range models {
		if strings.TrimSpace(alias) != "" && strings.TrimSpace(mapped) != "" {
			aliases = append(aliases, alias)
		}
	}
	if len(aliases) == 0 {
		return "", fmt.Errorf("provider.providers.<key>.models must contain at least one non-empty model mapping")
	}
	slices.Sort(aliases)
	return aliases[0], nil
}

func findProjectRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

func numberAsInt(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	default:
		return 0
	}
}
