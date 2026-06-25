package server

import (
	"testing"

	"moonbridge/internal/config"
	"moonbridge/internal/service/runtime"
)

func TestStrip1mSuffix(t *testing.T) {
	cases := []struct {
		in       string
		wantBase string
		wantHas  bool
	}{
		{"deepseek-v4-flash[1m]", "deepseek-v4-flash", true},
		{"deepseek-v4-flash[1M]", "deepseek-v4-flash", true},
		{"deepseek/deepseek-v4-flash[1m]", "deepseek/deepseek-v4-flash", true},
		{"deepseek-v4-flash", "deepseek-v4-flash", false},
		{"[1m]", "", true},
		{"", "", false},
		{"weird[1m]suffix", "weird[1m]suffix", false},
	}
	for _, c := range cases {
		base, has := strip1mSuffix(c.in)
		if base != c.wantBase || has != c.wantHas {
			t.Errorf("strip1mSuffix(%q) = (%q, %v), want (%q, %v)", c.in, base, has, c.wantBase, c.wantHas)
		}
	}
}

func newSuffixTestServer() *Server {
	cfg := config.Config{
		Routes: map[string]config.RouteEntry{
			"big":   {Provider: "p", Model: "big-upstream", ContextWindow: oneMillion},
			"small": {Provider: "p", Model: "small-upstream", ContextWindow: 200_000},
		},
		Models: map[string]config.ModelDef{
			"canonical-big": {ContextWindow: oneMillion},
		},
	}
	rt := runtime.NewRuntime(cfg, nil, nil)
	return &Server{runtime: rt, provider: &fakeStrictAnthropicClient{}}
}

func TestResolveModelOrFallback1mSuffix(t *testing.T) {
	s := newSuffixTestServer()

	// Genuine 1M model: [1m] accepted, suffix stripped, no error.
	route, model, err := s.resolveModelOrFallback("big[1m]")
	if err != nil {
		t.Fatalf("big[1m]: unexpected error %v", err)
	}
	if model != "big" {
		t.Errorf("big[1m]: clean model = %q, want %q", model, "big")
	}
	if pref, ok := route.Preferred(); !ok || pref.UpstreamModel == "big[1m]" {
		t.Errorf("big[1m]: upstream model must not carry suffix, got %+v", pref)
	}

	// Sub-1M model: [1m] rejected with an error.
	if _, _, err := s.resolveModelOrFallback("small[1m]"); err == nil {
		t.Error("small[1m]: expected error for sub-1M model, got nil")
	}

	// No suffix: behaves as before for both models.
	if _, model, err := s.resolveModelOrFallback("small"); err != nil || model != "small" {
		t.Errorf("small: got (%q, %v), want (%q, nil)", model, err, "small")
	}
	if _, model, err := s.resolveModelOrFallback("big"); err != nil || model != "big" {
		t.Errorf("big: got (%q, %v), want (%q, nil)", model, err, "big")
	}
}

func TestContextWindowForCanonicalModelDef(t *testing.T) {
	s := newSuffixTestServer()
	if cw := s.contextWindowFor("canonical-big", nil); cw != oneMillion {
		t.Errorf("contextWindowFor(canonical-big) = %d, want %d", cw, oneMillion)
	}
	if cw := s.contextWindowFor("unknown-model", nil); cw != 0 {
		t.Errorf("contextWindowFor(unknown-model) = %d, want 0", cw)
	}
}

func TestListModelsEmits1mVariantOnlyForGenuine1M(t *testing.T) {
	cfg := config.Config{
		ProviderDefs: map[string]config.ProviderDef{
			"p": {Models: map[string]config.ModelMeta{
				"big-model":   {ContextWindow: oneMillion},
				"small-model": {ContextWindow: 128_000},
			}},
		},
		Routes: map[string]config.RouteEntry{
			"big-route":   {Provider: "p", Model: "big-model", ContextWindow: oneMillion},
			"small-route": {Provider: "p", Model: "small-model", ContextWindow: 128_000},
		},
	}
	s := &Server{runtime: runtime.NewRuntime(cfg, nil, nil)}

	slugs := map[string]bool{}
	for _, m := range s.listModels() {
		if slug, ok := m["slug"].(string); ok {
			slugs[slug] = true
		}
	}

	want1m := []string{"p/big-model[1m]", "big-route[1m]"}
	for _, slug := range want1m {
		if !slugs[slug] {
			t.Errorf("listModels: missing expected 1M variant %q; got %v", slug, slugs)
		}
	}
	notWant := []string{"p/small-model[1m]", "small-route[1m]"}
	for _, slug := range notWant {
		if slugs[slug] {
			t.Errorf("listModels: unexpected 1M variant for sub-1M model %q", slug)
		}
	}
	// Base entries always present.
	for _, slug := range []string{"p/big-model", "p/small-model", "big-route", "small-route"} {
		if !slugs[slug] {
			t.Errorf("listModels: missing base entry %q", slug)
		}
	}
}
