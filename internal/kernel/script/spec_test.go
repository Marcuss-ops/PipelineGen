package script_test

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestGenerationSpecHasClips(t *testing.T) {
	tests := []struct {
		name     string
		spec     script.GenerationSpec
		expected bool
	}{
		{"no clips", script.GenerationSpec{}, false},
		{"clip IDs", script.GenerationSpec{ClipIDs: []string{"a"}}, true},
		{"num clips", script.GenerationSpec{NumClips: 5}, true},
		{"both", script.GenerationSpec{ClipIDs: []string{"a"}, NumClips: 5}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.spec.HasClips(); got != tt.expected {
				t.Errorf("HasClips() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestGenerationSpecHasText(t *testing.T) {
	tests := []struct {
		name     string
		spec     script.GenerationSpec
		expected bool
	}{
		{"empty", script.GenerationSpec{}, false},
		{"topic only", script.GenerationSpec{Topic: "AI"}, true},
		{"source only", script.GenerationSpec{SourceText: "text"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.spec.HasText(); got != tt.expected {
				t.Errorf("HasText() = %v, want %v", got, tt.expected)
			}
		})
	}
}
