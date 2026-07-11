package modelref_test

import (
	"testing"

	"moonbridge/internal/modelref"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name         string
		ref          string
		wantProvider string
		wantModel    string
	}{
		{
			name:         "provider slash model",
			ref:          "openai/gpt-4o",
			wantProvider: "openai",
			wantModel:    "gpt-4o",
		},
		{
			name:         "model paren provider",
			ref:          "claude-opus-4-6(kiro)",
			wantProvider: "kiro",
			wantModel:    "claude-opus-4-6",
		},
		{
			name:         "no separator returns empty provider and original ref",
			ref:          "gpt-4o",
			wantProvider: "",
			wantModel:    "gpt-4o",
		},
		{
			name:         "leading and trailing whitespace is trimmed",
			ref:          "  openai / gpt-4o  ",
			wantProvider: "openai",
			wantModel:    "gpt-4o",
		},
		{
			name:         "whitespace inside paren form is trimmed",
			ref:          "  claude ( kiro ) ",
			wantProvider: "kiro",
			wantModel:    "claude",
		},
		{
			name:         "paren form preferred over slash when both present",
			ref:          "anthropic/claude(kiro)",
			wantProvider: "kiro",
			wantModel:    "anthropic/claude",
		},
		{
			name:         "empty provider inside parens falls back to slash form",
			ref:          "provider/model()",
			wantProvider: "provider",
			wantModel:    "model()",
		},
		{
			name:         "paren form matches even when model contains a slash",
			ref:          "a/b(provider)",
			wantProvider: "provider",
			wantModel:    "a/b",
		},
		{
			name:         "open paren at index zero is not treated as paren form",
			ref:          "(provider)",
			wantProvider: "",
			wantModel:    "(provider)",
		},
		{
			name:         "open paren without closing suffix uses slash form",
			ref:          "model(provider",
			wantProvider: "",
			wantModel:    "model(provider",
		},
		{
			name:         "empty string",
			ref:          "",
			wantProvider: "",
			wantModel:    "",
		},
		{
			name:         "only slash yields empty provider and model",
			ref:          "/",
			wantProvider: "",
			wantModel:    "",
		},
		{
			name:         "slash with empty provider",
			ref:          "/model",
			wantProvider: "",
			wantModel:    "model",
		},
		{
			name:         "slash with empty model",
			ref:          "provider/",
			wantProvider: "provider",
			wantModel:    "",
		},
		{
			name:         "first slash is used to split",
			ref:          "a/b/c",
			wantProvider: "a",
			wantModel:    "b/c",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotProvider, gotModel := modelref.Parse(tt.ref)
			if gotProvider != tt.wantProvider {
				t.Errorf("Parse(%q) provider = %q, want %q", tt.ref, gotProvider, tt.wantProvider)
			}
			if gotModel != tt.wantModel {
				t.Errorf("Parse(%q) model = %q, want %q", tt.ref, gotModel, tt.wantModel)
			}
		})
	}
}
