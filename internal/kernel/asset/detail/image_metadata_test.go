package detail

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCanonicalImageMetadataBuilder_RetrievedSource(t *testing.T) {
	b := NewCanonicalImageMetadataBuilder("wikipedia", "").
		WithBaseInfo("A cat", "realistic", "abc123", []string{"cat"}, 100, 200)

	jsonStr, origin, provider := b.Build()
	if origin != asset.ImageOriginRetrieved {
		t.Errorf("origin = %q, want retrieved", origin)
	}
	if provider != asset.ProviderWikipedia {
		t.Errorf("provider = %q, want wikipedia", provider)
	}

	var meta map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &meta); err != nil {
		t.Fatal(err)
	}
	if meta["origin"] != "retrieved" {
		t.Errorf("meta.origin = %q, want retrieved", meta["origin"])
	}
	if meta["provider"] != "wikipedia" {
		t.Errorf("meta.provider = %q, want wikipedia", meta["provider"])
	}
	if meta["content_hash"] != "abc123" {
		t.Errorf("meta.content_hash = %q, want abc123", meta["content_hash"])
	}
}

func TestCanonicalImageMetadataBuilder_GeneratedSource(t *testing.T) {
	b := NewCanonicalImageMetadataBuilder("flux-2-klein", "").
		WithBaseInfo("A cat", "realistic", "abc123", []string{"cat"}, 100, 200)

	jsonStr, origin, provider := b.Build()
	if origin != asset.ImageOriginGenerated {
		t.Errorf("origin = %q, want generated", origin)
	}
	if provider != asset.ProviderFlux {
		t.Errorf("provider = %q, want flux", provider)
	}

	var meta map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &meta); err != nil {
		t.Fatal(err)
	}
	if meta["origin"] != "generated" {
		t.Errorf("meta.origin = %q, want generated", meta["origin"])
	}
	if meta["provider"] != "flux" {
		t.Errorf("meta.provider = %q, want flux", meta["provider"])
	}
}

func TestCanonicalImageMetadataBuilder_UploadSource(t *testing.T) {
	b := NewCanonicalImageMetadataBuilder("upload", "").
		WithBaseInfo("A cat", "realistic", "abc123", []string{"cat"}, 100, 200)

	jsonStr, origin, provider := b.Build()
	if origin != asset.ImageOriginUploaded {
		t.Errorf("origin = %q, want uploaded", origin)
	}
	if provider != asset.ProviderUpload {
		t.Errorf("provider = %q, want upload", provider)
	}

	var meta map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &meta); err != nil {
		t.Fatal(err)
	}
	if meta["origin"] != "uploaded" {
		t.Errorf("meta.origin = %q, want uploaded", meta["origin"])
	}
	if meta["provider"] != "upload" {
		t.Errorf("meta.provider = %q, want upload", meta["provider"])
	}
}

func TestCanonicalImageMetadataBuilder_ExtraPreservesClassifiedOriginProvider(t *testing.T) {
	extra := map[string]any{
		"search_text":     "cat",
		"prompt_original": "A cat",
		"style":           []string{"realistic"},
		"tags":            []string{"cat", "animal"},
		"origin":          "generated",
		"provider":        "flux",
	}

	b := NewCanonicalImageMetadataBuilder("wikipedia", "").
		WithBaseInfo("A cat", "realistic", "abc123", []string{"cat"}, 100, 200).
		WithExtra(extra)

	jsonStr, _, _ := b.Build()
	var meta map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &meta); err != nil {
		t.Fatal(err)
	}
	if meta["origin"] != "retrieved" {
		t.Errorf("meta.origin = %q, want retrieved (should not be overwritten by extra)", meta["origin"])
	}
	if meta["provider"] != "wikipedia" {
		t.Errorf("meta.provider = %q, want wikipedia (should not be overwritten by extra)", meta["provider"])
	}
	if meta["search_text"] != "cat" {
		t.Errorf("meta.search_text = %q, want cat", meta["search_text"])
	}
}

func TestCanonicalImageMetadataBuilder_ExtraMergesTags(t *testing.T) {
	extra := map[string]any{
		"tags": []string{"new"},
	}

	b := NewCanonicalImageMetadataBuilder("wikipedia", "").
		WithBaseInfo("A cat", "realistic", "abc123", []string{"cat"}, 100, 200).
		WithExtra(extra)

	jsonStr, _, _ := b.Build()
	var meta map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &meta); err != nil {
		t.Fatal(err)
	}
	tags, ok := meta["tags"].([]any)
	if !ok {
		t.Fatalf("tags type = %T, want []any", meta["tags"])
	}
	got := make([]string, 0, len(tags))
	for _, t := range tags {
		got = append(got, t.(string))
	}
	if !contains(got, "cat") || !contains(got, "new") {
		t.Errorf("tags = %v, want both cat and new", got)
	}
}

func TestAppendImageProvenance(t *testing.T) {
	updated := AppendImageProvenance("{}", "http://img", "http://page", "wikipedia", "cat")
	var meta map[string]any
	if err := json.Unmarshal([]byte(updated), &meta); err != nil {
		t.Fatal(err)
	}
	if meta["source_image_url"] != "http://img" {
		t.Errorf("source_image_url = %q, want http://img", meta["source_image_url"])
	}
	if meta["source_page_url"] != "http://page" {
		t.Errorf("source_page_url = %q, want http://page", meta["source_page_url"])
	}
	if meta["source_name"] != "wikipedia" {
		t.Errorf("source_name = %q, want wikipedia", meta["source_name"])
	}
	if meta["source_query"] != "cat" {
		t.Errorf("source_query = %q, want cat", meta["source_query"])
	}
}

func TestAppendImageProvenance_DoesNotOverwriteOriginProvider(t *testing.T) {
	updated := AppendImageProvenance(`{"origin":"retrieved","provider":"wikipedia"}`, "http://img", "", "", "")
	var meta map[string]any
	if err := json.Unmarshal([]byte(updated), &meta); err != nil {
		t.Fatal(err)
	}
	if meta["origin"] != "retrieved" {
		t.Errorf("origin = %q, want retrieved", meta["origin"])
	}
	if meta["provider"] != "wikipedia" {
		t.Errorf("provider = %q, want wikipedia", meta["provider"])
	}
}

func TestAppendImageProvenance_InvalidJSON(t *testing.T) {
	original := "not-json"
	updated := AppendImageProvenance(original, "http://img", "", "", "")
	if updated != original {
		t.Errorf("updated = %q, want original %q", updated, original)
	}
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if strings.EqualFold(s, item) {
			return true
		}
	}
	return false
}
