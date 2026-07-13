package webui_test

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ZhiYi-R/moon-bridge/internal/service/webui"
)

func TestHandlerServesConsoleIndex(t *testing.T) {
	handler := webui.NewHandler(testFS())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/console/", nil)
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if body := recorder.Body.String(); !strings.Contains(body, `<div id="root"></div>`) {
		t.Fatalf("body does not contain index marker: %s", body)
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.Contains(contentType, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", contentType)
	}
}

func TestHandlerFallsBackToIndexForClientRoute(t *testing.T) {
	handler := webui.NewHandler(testFS())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/console/providers/openai", nil)
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if body := recorder.Body.String(); !strings.Contains(body, "<title>Moon Bridge Console</title>") {
		t.Fatalf("body does not contain index title: %s", body)
	}
}

func TestHandlerServesStaticAsset(t *testing.T) {
	handler := webui.NewHandler(testFS())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/console/assets/app.js", nil)
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if body := recorder.Body.String(); body != `console.log("console asset");` {
		t.Fatalf("body = %q", body)
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.Contains(contentType, "javascript") {
		t.Fatalf("Content-Type = %q, want javascript", contentType)
	}
}

func TestEmbeddedReturnsHandler(t *testing.T) {
	handler := webui.Embedded()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/console/", nil)
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestEmbeddedIndexReferencesExistingAssets(t *testing.T) {
	handler := webui.Embedded()

	index := httptest.NewRecorder()
	handler.ServeHTTP(index, httptest.NewRequest(http.MethodGet, "/console/", nil))
	if index.Code != http.StatusOK {
		t.Fatalf("index status = %d, body = %s", index.Code, index.Body.String())
	}

	scriptSrcs := regexp.MustCompile(`src="(/console/assets/[^"]+)"`).FindAllStringSubmatch(index.Body.String(), -1)
	if len(scriptSrcs) == 0 {
		t.Fatalf("index does not reference any console script asset: %s", index.Body.String())
	}

	for _, match := range scriptSrcs {
		assetPath := match[1]
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, assetPath, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("asset %s status = %d, body = %s", assetPath, recorder.Code, recorder.Body.String())
		}
		if contentType := recorder.Header().Get("Content-Type"); !strings.Contains(contentType, "javascript") {
			t.Fatalf("asset %s Content-Type = %q, want javascript", assetPath, contentType)
		}
		if recorder.Body.Len() == 0 {
			t.Fatalf("asset %s is empty", assetPath)
		}
	}
}

func TestEmbeddedConsoleIncludesModelReasoningSupportSwitch(t *testing.T) {
	scripts := embeddedScriptBodies(t)
	combined := strings.Join(scripts, "\n")

	if !strings.Contains(combined, "supports_reasoning") {
		t.Fatalf("embedded console scripts do not include supports_reasoning; run make webui-build to sync the bundled webui")
	}
	if !strings.Contains(combined, "Model reasoning support field") {
		t.Fatalf("embedded console scripts do not include the model reasoning switch guard; run make webui-build to sync the bundled webui")
	}
}

func embeddedScriptBodies(t *testing.T) []string {
	t.Helper()

	handler := webui.Embedded()

	index := httptest.NewRecorder()
	handler.ServeHTTP(index, httptest.NewRequest(http.MethodGet, "/console/", nil))
	if index.Code != http.StatusOK {
		t.Fatalf("index status = %d, body = %s", index.Code, index.Body.String())
	}

	scriptSrcs := regexp.MustCompile(`src="(/console/assets/[^"]+)"`).FindAllStringSubmatch(index.Body.String(), -1)
	if len(scriptSrcs) == 0 {
		t.Fatalf("index does not reference any console script asset: %s", index.Body.String())
	}

	scripts := make([]string, 0, len(scriptSrcs))
	for _, match := range scriptSrcs {
		assetPath := match[1]
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, assetPath, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("asset %s status = %d, body = %s", assetPath, recorder.Code, recorder.Body.String())
		}
		scripts = append(scripts, recorder.Body.String())
	}
	return scripts
}

func testFS() fs.FS {
	return fstest.MapFS{
		"index.html": &fstest.MapFile{
			Data: []byte(`<!doctype html><html><head><title>Moon Bridge Console</title></head><body><div id="root"></div><script type="module" src="/console/assets/app.js"></script></body></html>`),
		},
		"assets/app.js": &fstest.MapFile{
			Data: []byte(`console.log("console asset");`),
		},
	}
}
