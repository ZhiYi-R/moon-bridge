package trace_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	srvtrace "moonbridge/internal/service/server/trace"
	mbtrace "moonbridge/internal/service/trace"
)

func newTracer(t *testing.T, enabled bool) (*mbtrace.Tracer, string) {
	t.Helper()
	root := t.TempDir()
	return mbtrace.New(mbtrace.Config{Enabled: enabled, Root: root, SessionID: "sess", Flat: true}), root
}

// categoryFiles returns the JSON files written under root/<category>.
func categoryFiles(t *testing.T, root, category string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, category))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", category, err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestWriteTraceDispatchesByCategory(t *testing.T) {
	tests := []struct {
		name     string
		record   mbtrace.Record
		wantDirs []string
	}{
		{
			name:     "chat only",
			record:   mbtrace.Record{ChatRequest: map[string]any{"k": "v"}},
			wantDirs: []string{"Chat"},
		},
		{
			name:     "response via openai request",
			record:   mbtrace.Record{OpenAIRequest: map[string]any{"k": "v"}},
			wantDirs: []string{"Response"},
		},
		{
			name:     "response via upstream request",
			record:   mbtrace.Record{UpstreamRequest: map[string]any{"k": "v"}},
			wantDirs: []string{"Response"},
		},
		{
			name:     "anthropic only",
			record:   mbtrace.Record{AnthropicResponse: map[string]any{"k": "v"}},
			wantDirs: []string{"Anthropic"},
		},
		{
			name: "all three categories",
			record: mbtrace.Record{
				ChatRequest:      map[string]any{"k": "v"},
				OpenAIRequest:    map[string]any{"k": "v"},
				AnthropicRequest: map[string]any{"k": "v"},
			},
			wantDirs: []string{"Chat", "Response", "Anthropic"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracer, root := newTracer(t, true)
			var errBuf bytes.Buffer
			w := srvtrace.NewFileWriter(tracer, &errBuf)

			w.WriteTrace(tt.record)

			for _, dir := range tt.wantDirs {
				if files := categoryFiles(t, root, dir); len(files) == 0 {
					t.Errorf("expected a trace file under %q, found none", dir)
				}
			}
			for _, dir := range []string{"Chat", "Response", "Anthropic"} {
				if !contains(tt.wantDirs, dir) {
					if files := categoryFiles(t, root, dir); len(files) != 0 {
						t.Errorf("did not expect trace files under %q, got %v", dir, files)
					}
				}
			}
			if errBuf.Len() != 0 {
				t.Errorf("unexpected error output: %q", errBuf.String())
			}
		})
	}
}

func TestWriteTraceNoCategoryWritesNothing(t *testing.T) {
	tracer, root := newTracer(t, true)
	w := srvtrace.NewFileWriter(tracer, nil)

	// A record with no request/response payloads matches no category.
	w.WriteTrace(mbtrace.Record{Model: "m"})

	for _, dir := range []string{"Chat", "Response", "Anthropic"} {
		if files := categoryFiles(t, root, dir); len(files) != 0 {
			t.Errorf("expected no files under %q, got %v", dir, files)
		}
	}
}

func TestWriteTraceDisabledTracer(t *testing.T) {
	tracer, root := newTracer(t, false)
	w := srvtrace.NewFileWriter(tracer, nil)

	w.WriteTrace(mbtrace.Record{ChatRequest: map[string]any{"k": "v"}})

	if entries, err := os.ReadDir(root); err == nil && len(entries) != 0 {
		t.Errorf("disabled tracer should not write, found %d entries", len(entries))
	}
}

func TestWriteTraceNilTracer(t *testing.T) {
	w := srvtrace.NewFileWriter(nil, nil)
	// Must be a no-op and must not panic.
	w.WriteTrace(mbtrace.Record{ChatRequest: map[string]any{"k": "v"}})
}

func TestWriteCategoryWritesFile(t *testing.T) {
	tracer, root := newTracer(t, true)
	w := srvtrace.NewFileWriter(tracer, nil)

	w.WriteCategory("Response", 7, mbtrace.Record{OpenAIRequest: map[string]any{"k": "v"}})

	path := filepath.Join(root, "Response", "7.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected trace file at %s: %v", path, err)
	}
}

func TestWriteCategoryReportsError(t *testing.T) {
	// Point the tracer root at a path whose parent is a regular file so that
	// MkdirAll fails, exercising the error-reporting branch.
	root := t.TempDir()
	filePath := filepath.Join(root, "notadir")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	tracer := mbtrace.New(mbtrace.Config{Enabled: true, Root: filePath, SessionID: "sess", Flat: true})
	var errBuf bytes.Buffer
	w := srvtrace.NewFileWriter(tracer, &errBuf)

	w.WriteCategory("Response", 1, mbtrace.Record{OpenAIRequest: map[string]any{"k": "v"}})

	if errBuf.Len() == 0 {
		t.Error("expected an error to be written to the error writer")
	}
}

// Ensure FileWriter satisfies the Writer interface.
var _ srvtrace.Writer = (*srvtrace.FileWriter)(nil)

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
