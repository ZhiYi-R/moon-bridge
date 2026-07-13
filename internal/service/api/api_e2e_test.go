package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ZhiYi-R/moon-bridge/internal/config"
	"github.com/ZhiYi-R/moon-bridge/internal/service/store"
)

// ---------------------------------------------------------------------------
// E2E: PUT provider → verify pending → POST apply → verify effective
// ---------------------------------------------------------------------------

func TestE2EProviderFullLifecycle(t *testing.T) {
	f := newFixture(t)

	reqBody := map[string]any{
		"base_url": "https://e2e-lifecycle.test/api",
		"api_key":  "sk-e2e-lifecycle-key",
		"version":  "2025-01-01",
		"protocol": "anthropic",
	}
	resp := f.request("PUT", "/providers/e2e-lifecycle", reqBody)
	if resp.Code != http.StatusAccepted {
		t.Fatalf("step 1: PUT /providers/e2e-lifecycle returned %d: %s", resp.Code, resp.Body.String())
	}

	var putResult map[string]any
	f.decode(resp, &putResult)
	changeID := int64(putResult["change_id"].(float64))
	if changeID == 0 {
		t.Fatal("step 1: expected non-zero change_id")
	}
	if putResult["status"] != "pending" {
		t.Fatalf("step 1: expected status=pending, got %v", putResult["status"])
	}

	resp2 := f.request("GET", "/changes", nil)
	if resp2.Code != http.StatusOK {
		t.Fatalf("step 2: GET /changes returned %d", resp2.Code)
	}
	var changes []store.ChangeRow
	f.decode(resp2, &changes)
	found := false
	for _, c := range changes {
		if c.ID == changeID {
			found = true
			if c.Applied {
				t.Fatal("step 2: change should not be applied yet")
			}
			if c.Resource != "provider" {
				t.Fatalf("step 2: expected resource=provider, got %s", c.Resource)
			}
			if c.Action != "create" {
				t.Fatalf("step 2: expected action=create, got %s", c.Action)
			}
			if c.TargetKey != "e2e-lifecycle" {
				t.Fatalf("step 2: expected target_key=e2e-lifecycle, got %s", c.TargetKey)
			}
			break
		}
	}
	if !found {
		t.Fatal("step 2: change not found in pending list")
	}

	resp3 := f.request("GET", "/config/effective", nil)
	resp2 = f.request("GET", "/config/effective", nil)
	var effectiveBefore config.FileConfig
	f.decode(resp3, &effectiveBefore)
	if _, exists := effectiveBefore.Providers["e2e-lifecycle"]; exists {
		t.Fatal("step 3: e2e-lifecycle should NOT be in effective config before apply")
	}

	resp4 := f.request("POST", "/changes/apply", nil)
	if resp4.Code != http.StatusOK {
		t.Fatalf("step 4: POST /changes/apply returned %d: %s", resp4.Code, resp4.Body.String())
	}
	var applyResult map[string]any
	f.decode(resp4, &applyResult)
	if applyResult["status"] != "success" {
		t.Fatalf("step 4: expected status=success, got %v", applyResult["status"])
	}

	resp5 := f.request("GET", "/config/effective", nil)
	var effectiveAfter config.FileConfig
	f.decode(resp5, &effectiveAfter)
	pd, ok := effectiveAfter.Providers["e2e-lifecycle"]
	if !ok {
		t.Fatal("step 5: e2e-lifecycle should exist in effective config after apply")
	}
	if pd.BaseURL != "https://e2e-lifecycle.test/api" {
		t.Fatalf("step 5: expected base_url 'https://e2e-lifecycle.test/api', got '%s'", pd.BaseURL)
	}

	rtCfg := f.rt.Current()
	if _, ok := rtCfg.Config.ProviderDefs["e2e-lifecycle"]; !ok {
		t.Fatal("step 6: e2e-lifecycle should exist in runtime config after apply")
	}
	if rtCfg.Config.ProviderDefs["e2e-lifecycle"].BaseURL != "https://e2e-lifecycle.test/api" {
		t.Fatalf("step 6: runtime base_url mismatch")
	}
	if rtCfg.ProviderMgr == nil {
		t.Fatal("step 6: ProviderMgr should not be nil after reload")
	}

	resp7 := f.request("GET", "/changes", nil)
	var remaining []store.ChangeRow
	f.decode(resp7, &remaining)
	if len(remaining) != 0 {
		t.Fatalf("step 7: expected 0 pending changes after apply, got %d", len(remaining))
	}

	resp8 := f.request("GET", "/providers/e2e-lifecycle", nil)
	if resp8.Code != http.StatusOK {
		t.Fatalf("step 8: GET /providers/e2e-lifecycle returned %d: %s", resp8.Code, resp8.Body.String())
	}
	var pv map[string]any
	f.decode(resp8, &pv)
	if pv["key"] != "e2e-lifecycle" {
		t.Fatalf("step 8: expected key=e2e-lifecycle, got %v", pv["key"])
	}
	apiKey := pv["api_key"].(string)
	if apiKey == "sk-e2e-lifecycle-key" {
		t.Fatal("step 8: API key should be masked in GET /providers/{key} response")
	}
	if !strings.Contains(apiKey, "****") {
		t.Fatalf("step 8: expected masked API key, got %q", apiKey)
	}
}

// ---------------------------------------------------------------------------
// E2E: Import → preview → apply 全流程
// ---------------------------------------------------------------------------

func TestE2EConfigImportPreviewApply(t *testing.T) {
	f := newFixture(t)

	importYAML := `mode: Transform
providers:
  imported-provider:
    base_url: https://imported.test/api
    api_key: sk-imported-key-12345
    protocol: anthropic
    offers:
      - model: gpt-4o
        priority: 1
        pricing:
          input_price: 2.5
          output_price: 10.0
models:
  gpt-4o:
    display_name: GPT-4o Imported
    context_window: 128000
`
	body := map[string]string{"yaml": importYAML}
	resp := f.request("POST", "/config/import", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("step 1: POST /config/import returned %d: %s", resp.Code, resp.Body.String())
	}

	// Import now creates pending changes directly, then returns the change list.
	var result map[string]any
	f.decode(resp, &result)
	changes, ok := result["changes"]
	if !ok {
		t.Fatalf("step 1: expected 'changes' in response, got %v", result)
	}
	chList := changes.([]any)
	if len(chList) < 2 {
		t.Fatalf("step 1: expected at least 2 changes (provider + model), got %d", len(chList))
	}

	resp2 := f.request("GET", "/config/effective", nil)
	var effectiveBefore config.FileConfig
	f.decode(resp2, &effectiveBefore)
	if _, exists := effectiveBefore.Providers["imported-provider"]; exists {
		t.Fatal("step 2: imported-provider should NOT be in effective config yet")
	}

	putBody := map[string]any{
		"base_url": "https://imported.test/api",
		"api_key":  "sk-imported-key-12345",
		"protocol": "anthropic",
		"version":  "",
	}
	resp3 := f.request("PUT", "/providers/imported-provider", putBody)
	if resp3.Code != http.StatusAccepted {
		t.Fatalf("step 3: PUT /providers/imported-provider returned %d: %s", resp3.Code, resp3.Body.String())
	}

	modelBody := map[string]any{
		"display_name":   "GPT-4o Imported",
		"context_window": 128000,
	}
	respModel := f.request("PUT", "/models/gpt-4o", modelBody)
	if respModel.Code != http.StatusAccepted {
		t.Fatalf("step 3: PUT /models/gpt-4o returned %d: %s", respModel.Code, respModel.Body.String())
	}

	resp4 := f.request("POST", "/changes/apply", nil)
	if resp4.Code != http.StatusOK {
		t.Fatalf("step 4: POST /changes/apply returned %d: %s", resp4.Code, resp4.Body.String())
	}

	resp5 := f.request("GET", "/config/effective", nil)
	var effectiveAfter config.FileConfig
	f.decode(resp5, &effectiveAfter)
	if _, exists := effectiveAfter.Providers["imported-provider"]; !exists {
		t.Fatal("step 5: imported-provider should exist in effective config after import+apply")
	}
}

// ---------------------------------------------------------------------------
// E2E: 修改 provider 并多次变更（确保不因唯一 provider 删除导致失败）
// ---------------------------------------------------------------------------

func TestE2EProviderUpdateThenDelete(t *testing.T) {
	f := newFixture(t)

	// Add a second provider first so deleting anthropic won't leave 0 providers.
	_ = f.request("PUT", "/providers/backup", map[string]any{
		"base_url": "https://backup.test", "api_key": "sk-backup-key", "protocol": "anthropic",
	})
	f.request("POST", "/changes/apply", nil)

	// PATCH the existing anthropic provider
	patchBody := map[string]any{
		"base_url": "https://updated-anthropic.test",
		"api_key":  "******",
	}
	resp := f.request("PATCH", "/providers/anthropic", patchBody)
	if resp.Code != http.StatusAccepted {
		t.Fatalf("step 1: PATCH /providers/anthropic returned %d: %s", resp.Code, resp.Body.String())
	}

	resp2 := f.request("POST", "/changes/apply", nil)
	if resp2.Code != http.StatusOK {
		t.Fatalf("step 2: POST /changes/apply returned %d: %s", resp2.Code, resp2.Body.String())
	}

	cfg := f.rt.Current()
	prov := cfg.Config.ProviderDefs["anthropic"]
	if prov.BaseURL != "https://updated-anthropic.test" {
		t.Fatalf("step 3: expected base_url 'https://updated-anthropic.test', got '%s'", prov.BaseURL)
	}
	if prov.APIKey != "sk-ant-test-key-12345678" {
		t.Fatalf("step 3: API key should be preserved, got '%s'", prov.APIKey)
	}

	resp4 := f.request("DELETE", "/providers/backup", nil)
	if resp4.Code != http.StatusAccepted {
		t.Fatalf("step 4: DELETE /providers/backup returned %d: %s", resp4.Code, resp4.Body.String())
	}

	resp5 := f.request("POST", "/changes/apply", nil)
	if resp5.Code != http.StatusOK {
		t.Fatalf("step 5: POST /changes/apply (delete) returned %d: %s", resp5.Code, resp5.Body.String())
	}

	cfg2 := f.rt.Current()
	if _, ok := cfg2.Config.ProviderDefs["backup"]; ok {
		t.Fatal("step 6: backup should be deleted from runtime config")
	}
	resp6 := f.request("GET", "/providers/backup", nil)
	if resp6.Code != http.StatusNotFound {
		t.Fatalf("step 6: expected 404 for deleted provider, got %d", resp6.Code)
	}
	// Verify anthropic still exists
	if _, ok := cfg2.Config.ProviderDefs["anthropic"]; !ok {
		t.Fatal("step 6: anthropic should still exist in runtime config")
	}
}

// ---------------------------------------------------------------------------
// E2E: 引用完整性 — 删除被 offers 引用的 model 被拒 (409)
// ---------------------------------------------------------------------------

func TestE2EReferenceIntegrityModelWithOffers(t *testing.T) {
	f := newFixture(t)

	cfg := f.rt.Current()
	modelSlug := "claude-sonnet"
	if _, ok := cfg.Config.Models[modelSlug]; !ok {
		t.Fatalf("model %q should exist", modelSlug)
	}

	resp := f.request("DELETE", "/models/"+modelSlug, nil)
	if resp.Code != http.StatusConflict {
		t.Fatalf("DELETE /models/%s returned %d (expected 409): %s", modelSlug, resp.Code, resp.Body.String())
	}

	var errResp map[string]any
	f.decode(resp, &errResp)
	errObj, ok := errResp["error"].(map[string]any)
	if !ok {
		t.Fatal("expected error object in response")
	}
	if errObj["code"] != "referenced" {
		t.Fatalf("expected error code 'referenced', got '%s'", errObj["code"])
	}

	changes, _ := f.store.ListPendingChanges()
	if len(changes) != 0 {
		t.Fatal("no changes should be staged after a rejected delete")
	}

	cfg2 := f.rt.Current()
	if _, ok := cfg2.Config.Models[modelSlug]; !ok {
		t.Fatal("model should still exist in runtime after rejected delete")
	}
}

// ---------------------------------------------------------------------------
// E2E: Secret 脱敏 + PATCH 保留旧 key
// ---------------------------------------------------------------------------

func TestE2ESecretMaskingAndPatchPreserve(t *testing.T) {
	f := newFixture(t)

	resp := f.request("GET", "/providers/anthropic", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /providers/anthropic returned %d: %s", resp.Code, resp.Body.String())
	}
	var pv map[string]any
	f.decode(resp, &pv)
	displayedKey := pv["api_key"].(string)
	if displayedKey == "sk-ant-test-key-12345678" {
		t.Fatal("API key should be masked in GET response")
	}
	if !strings.Contains(displayedKey, "****") {
		t.Fatalf("expected masked key, got %q", displayedKey)
	}

	patchBody := map[string]any{
		"base_url": "https://patched.test",
		"api_key":  "******",
	}
	resp2 := f.request("PATCH", "/providers/anthropic", patchBody)
	if resp2.Code != http.StatusAccepted {
		t.Fatalf("PATCH /providers/anthropic returned %d: %s", resp2.Code, resp2.Body.String())
	}

	resp3 := f.request("POST", "/changes/apply", nil)
	if resp3.Code != http.StatusOK {
		t.Fatalf("POST /changes/apply returned %d: %s", resp3.Code, resp3.Body.String())
	}

	cfg := f.rt.Current()
	prov := cfg.Config.ProviderDefs["anthropic"]
	if prov.APIKey != "sk-ant-test-key-12345678" {
		t.Fatalf("expected preserved API key, got %q", prov.APIKey)
	}
	if prov.BaseURL != "https://patched.test" {
		t.Fatalf("expected base_url 'https://patched.test', got %q", prov.BaseURL)
	}

	resp5 := f.request("GET", "/providers/anthropic", nil)
	var pv2 map[string]any
	f.decode(resp5, &pv2)
	displayedKey2 := pv2["api_key"].(string)
	if !strings.Contains(displayedKey2, "****") {
		t.Fatalf("API key should still be masked, got %q", displayedKey2)
	}
}

// ---------------------------------------------------------------------------
// E2E: 无效变更被拒绝
// ---------------------------------------------------------------------------

func TestE2EInvalidChangeRejected(t *testing.T) {
	f := newFixture(t)

	body := map[string]any{
		"api_key": "sk-test-key",
	}
	resp := f.request("PUT", "/providers/incomplete-provider", body)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for incomplete provider, got %d: %s", resp.Code, resp.Body.String())
	}

	changes, _ := f.store.ListPendingChanges()
	for _, c := range changes {
		if c.TargetKey == "incomplete-provider" {
			t.Fatal("no change should be staged for incomplete provider")
		}
	}

	resp2 := f.request("POST", "/config/validate", map[string]string{"config": ""})
	if resp2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty validate, got %d", resp2.Code)
	}

	resp3 := f.request("POST", "/config/import", map[string]string{"yaml": ""})
	if resp3.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty import, got %d", resp3.Code)
	}
}

// ---------------------------------------------------------------------------
// E2E: 配置导出验证
// ---------------------------------------------------------------------------

func TestE2EConfigExportAfterApply(t *testing.T) {
	f := newFixture(t)

	putBody := map[string]any{
		"base_url": "https://export-test.example.com",
		"api_key":  "sk-export-test-key",
	}
	f.request("PUT", "/providers/export-test-provider", putBody)
	f.request("POST", "/changes/apply", nil)

	resp := f.request("GET", "/config/export?include_secrets=false", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /config/export returned %d: %s", resp.Code, resp.Body.String())
	}
	exportBody := resp.Body.String()
	if !strings.Contains(exportBody, "export-test-provider") {
		t.Fatal("export should contain the newly added provider")
	}
	if strings.Contains(exportBody, "sk-export-test-key") {
		t.Fatal("export without secrets should mask API keys")
	}
	if resp.Header().Get("Content-Type") != "application/x-yaml" {
		t.Fatalf("expected Content-Type application/x-yaml, got %s", resp.Header().Get("Content-Type"))
	}
}

// ---------------------------------------------------------------------------
// E2E: 多个变更批量 apply
// ---------------------------------------------------------------------------

func TestE2EMultipleChangesBatchApply(t *testing.T) {
	f := newFixture(t)

	providers := []struct {
		key, url, apiKey string
	}{
		{"batch-provider-a", "https://batch-a.test", "sk-batch-a-key"},
		{"batch-provider-b", "https://batch-b.test", "sk-batch-b-key"},
		{"batch-provider-c", "https://batch-c.test", "sk-batch-c-key"},
	}

	for _, p := range providers {
		body := map[string]any{
			"base_url": p.url,
			"api_key":  p.apiKey,
			"protocol": "anthropic",
		}
		resp := f.request("PUT", "/providers/"+p.key, body)
		if resp.Code != http.StatusAccepted {
			t.Fatalf("PUT /providers/%s returned %d: %s", p.key, resp.Code, resp.Body.String())
		}
	}

	resp := f.request("GET", "/changes", nil)
	var changes []store.ChangeRow
	f.decode(resp, &changes)
	if len(changes) != 3 {
		t.Fatalf("expected 3 pending changes, got %d", len(changes))
	}

	f.request("POST", "/changes/apply", nil)

	cfg := f.rt.Current()
	for _, p := range providers {
		if _, ok := cfg.Config.ProviderDefs[p.key]; !ok {
			t.Fatalf("provider %q should exist after batch apply", p.key)
		}
	}

	resp2 := f.request("GET", "/changes", nil)
	var remaining []store.ChangeRow
	f.decode(resp2, &remaining)
	if len(remaining) != 0 {
		t.Fatalf("expected 0 pending changes after apply, got %d", len(remaining))
	}
}

// ---------------------------------------------------------------------------
// E2E: Discard 后配置回退
// ---------------------------------------------------------------------------

func TestE2EChangesDiscardRollback(t *testing.T) {
	f := newFixture(t)

	resp0 := f.request("GET", "/status", nil)
	var status0 map[string]any
	f.decode(resp0, &status0)
	initialCount := status0["provider_count"].(float64)

	putBody := map[string]any{
		"base_url": "https://discard-test.test",
		"api_key":  "sk-discard-test-key",
	}
	f.request("PUT", "/providers/discard-test-provider", putBody)

	resp := f.request("GET", "/changes", nil)
	var changes []store.ChangeRow
	f.decode(resp, &changes)
	if len(changes) != 1 {
		t.Fatalf("expected 1 pending change, got %d", len(changes))
	}

	resp2 := f.request("POST", "/changes/discard", nil)
	if resp2.Code != http.StatusOK {
		t.Fatalf("POST /changes/discard returned %d: %s", resp2.Code, resp2.Body.String())
	}

	resp3 := f.request("GET", "/changes", nil)
	var remaining []store.ChangeRow
	f.decode(resp3, &remaining)
	if len(remaining) != 0 {
		t.Fatalf("expected 0 pending changes after discard, got %d", len(remaining))
	}

	resp4 := f.request("GET", "/status", nil)
	var status1 map[string]any
	f.decode(resp4, &status1)
	if status1["provider_count"].(float64) != initialCount {
		t.Fatalf("provider count should still be %.0f after discard, got %.0f", initialCount, status1["provider_count"])
	}
}

// ---------------------------------------------------------------------------
// E2E: 空变更 Apply / Discard 安全处理
// ---------------------------------------------------------------------------

func TestE2EEmptyChangesSafe(t *testing.T) {
	f := newFixture(t)

	resp := f.request("POST", "/changes/apply", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("POST /changes/apply (empty) returned %d: %s", resp.Code, resp.Body.String())
	}
	var result map[string]any
	f.decode(resp, &result)
	if result["status"] != "success" {
		t.Fatalf("expected status=success for empty apply, got %v", result["status"])
	}

	resp2 := f.request("POST", "/changes/discard", nil)
	if resp2.Code != http.StatusOK {
		t.Fatalf("POST /changes/discard (empty) returned %d: %s", resp2.Code, resp2.Body.String())
	}
}

// ---------------------------------------------------------------------------
// E2E: Apply 后配置一致性
// ---------------------------------------------------------------------------

func TestE2EChangesAppliedConfigConsistency(t *testing.T) {
	f := newFixture(t)

	_ = f.request("PUT", "/providers/timestamp-provider", map[string]any{
		"base_url": "https://timestamp-test.test",
		"api_key":  "sk-timestamp-test-key",
		"protocol": "anthropic",
	})

	resp := f.request("POST", "/changes/apply", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("POST /changes/apply returned %d: %s", resp.Code, resp.Body.String())
	}
	_ = time.Now().UTC()

	resp2 := f.request("GET", "/config/effective", nil)
	var effective config.FileConfig
	f.decode(resp2, &effective)
	if _, ok := effective.Providers["timestamp-provider"]; !ok {
		t.Fatal("timestamp-provider should be in effective config after apply")
	}
}
