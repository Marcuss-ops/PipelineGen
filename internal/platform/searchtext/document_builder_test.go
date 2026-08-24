package searchtext

import (
	"context"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

func TestAssetSearchDocumentBuilder_Build_IncludesAllFields(t *testing.T) {
	a := asset.Asset{
		ID:       "clip-1",
		Source:   "artlist",
		Name:     "Maya Temple",
		Category: "nature",
		Metadata: map[string]any{
			"description":         "Ancient Maya temple in the jungle",
			"creator":             "John Doe",
			"provider_categories": []string{"history", "travel"},
			"categories":          []string{"cinematic"},
			"location":            "Guatemala",
			"scene_type":          "ancient ruins",
			"vlm_ocr_text":        "Maya",
			"text_on_screen":      "temple",
		},
	}
	a.ProviderTags = []string{"maya", "temple"}
	a.VLMTags = []string{"jungle", "ruins"}

	builder := NewAssetSearchDocumentBuilder(nil)
	text, err := builder.Build(context.Background(), a)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	want := []string{
		"Maya Temple",
		"nature",
		"Ancient Maya temple in the jungle",
		"John Doe",
		"Guatemala",
		"ancient ruins",
		"Maya",
		"temple",
		"maya",
		"temple",
		"jungle",
		"ruins",
		"history",
		"travel",
		"cinematic",
	}
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("Build() missing %q in %q", w, text)
		}
	}
}

func TestAssetSearchDocumentBuilder_Build_EmptyAssetID(t *testing.T) {
	builder := NewAssetSearchDocumentBuilder(nil)
	_, err := builder.Build(context.Background(), asset.Asset{})
	if err == nil {
		t.Fatal("Build(empty asset) must error")
	}
}

func TestAssetSearchDocumentBuilder_Build_WithBase(t *testing.T) {
	a := asset.Asset{
		ID:       "clip-2",
		Source:   "artlist",
		Name:     "Drone Shot",
		Category: "nature",
		Metadata: map[string]any{
			"description": "Over mountains",
		},
	}
	a.ProviderTags = []string{"drone"}

	base := NewRegistry()
	builder := NewAssetSearchDocumentBuilder(base)
	text, err := builder.Build(context.Background(), a)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	if !strings.Contains(text, "Drone Shot") {
		t.Errorf("Build() missing title; got %q", text)
	}
	if !strings.Contains(text, "drone") {
		t.Errorf("Build() missing provider tag; got %q", text)
	}
}
