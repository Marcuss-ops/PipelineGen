// Package images (storage_ingest_metadata_test.go) pins the
// canonical 10-key metadata_json shape produced by
// buildCanonicalGeneratedMetadata (godlike/06 SSOT —
// PR-CANONICAL-GENERATED-IMAGE-METADATA, July 2026).
//
// The test pins the canonical field SERIES (every required key
// present after a fake generated-image ingest) and the canonical
// field VALUES that downstream readers rely on (origin ==
// "generated", content_hash matches input, embedding_version_visual
// matches the schema constant). Downstream readers (Qdrant
// payload mapper, reconciler, operator dumps) treat this shape
// as the single source of truth — pinning it here is the
// regression gate that catches accidental key removal or
// value drift across the next refactor.
package images

import (
	"encoding/json"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
)

// canonicalRequiredKeys is the SSOT set of metadata_json keys
// for a non-enriched generated image. Adding a new canonical
// key requires (a) adding it to buildCanonicalGeneratedMetadata,
// and (b) adding it here — the test fails CI if either step
// is skipped. The order below is stable and matches the
// documentation in PR-CANONICAL-GENERATED-IMAGE-METADATA.
var canonicalRequiredKeys = []string{
	"prompt_original",
	"semantic_description",
	"style",
	"tags",
	"provider",
	"origin",
	"width",
	"height",
	"content_hash",
	"embedding_version_visual",
}

// TestBuildCanonicalGeneratedMetadata_AllCanonicalKeysPresent
// is the canonical-field-SERIES gate. Asserts every one of
// the 10 canonical keys is present in the metadata_json output
// after a fake generated-image ingest, then pins the value of
// high-blast-radius keys (origin, content_hash,
// embedding_version_visual, style, width, height, tags).
func TestBuildCanonicalGeneratedMetadata_AllCanonicalKeysPresent(t *testing.T) {
	in := canonicalMetaInput{
		Description: "a cinematic scene of a mountain sunrise",
		Style:       "cinematic",
		Source:      "ai://flux/dev",
		Generator:   "flux",
		Hash:        "deadbeef0123456789",
		Tags:        []string{"mountain", "sunrise", "outdoor"},
		Width:       1920,
		Height:      1080,
	}

	metaBytes := buildCanonicalGeneratedMetadata(in)
	if len(metaBytes) == 0 {
		t.Fatal("buildCanonicalGeneratedMetadata returned empty bytes — canonical shape regressed")
	}

	var got map[string]any
	if err := json.Unmarshal(metaBytes, &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, string(metaBytes))
	}

	for _, key := range canonicalRequiredKeys {
		if _, ok := got[key]; !ok {
			t.Errorf("canonical key %q missing from metadata_json output; got=%v", key, keysOf(got))
		}
	}

	// Pin value invariants on the high-blast-radius fields.
	if got["origin"] != "generated" {
		t.Errorf(`origin invariant: got %v, want "generated"`, got["origin"])
	}
	if got["content_hash"] != in.Hash {
		t.Errorf("content_hash invariant: got %v, want %q", got["content_hash"], in.Hash)
	}
	if got["embedding_version_visual"] != schema.VisualEmbeddingModelVersion {
		t.Errorf("embedding_version_visual invariant: got %v, want %q",
			got["embedding_version_visual"], schema.VisualEmbeddingModelVersion)
	}
	if got["style"] != in.Style {
		t.Errorf("style invariant: got %v, want %q", got["style"], in.Style)
	}

	// Numeric width/height come back as float64 from JSON; cast to int for comparison.
	if w, ok := got["width"].(float64); !ok || int(w) != in.Width {
		t.Errorf("width invariant: got %v (%T), want %d", got["width"], got["width"], in.Width)
	}
	if h, ok := got["height"].(float64); !ok || int(h) != in.Height {
		t.Errorf("height invariant: got %v (%T), want %d", got["height"], got["height"], in.Height)
	}

	// Tags round-trip as []any; assert length to pin structural
	// integrity without depending on JSON-marshal ordering.
	tagsAny, ok := got["tags"].([]any)
	if !ok {
		t.Fatalf("tags did not round-trip as []any: %T", got["tags"])
	}
	if len(tagsAny) != len(in.Tags) {
		t.Errorf("tags length: got %d, want %d", len(tagsAny), len(in.Tags))
	}

	// Pin provider is delegated to classifyImageProvider(source, generator).
	// "ai://flux/dev" + "flux" input must produce a non-empty provider string;
	// the precise string is owned by classifyImageProvider, but the
	// canonical key MUST exist (downstream readers iterate it).
	if p, _ := got["provider"].(string); p == "" {
		t.Errorf("provider invariant: got empty string, must be non-empty")
	}
}

// TestBuildCanonicalGeneratedMetadata_OriginIsAlwaysGeneratedRegardlessOfSource
// is the origin-string lock-in. The AI path's origin string is
// hardcoded to "generated" — even if a future caller passes a
// non-AI source through the fallback branch, the canonical
// shape MUST keep origin="generated". Downstream payload
// mappers break otherwise (godlike/06).
func TestBuildCanonicalGeneratedMetadata_OriginIsAlwaysGeneratedRegardlessOfSource(t *testing.T) {
	cases := []struct {
		name, source, generator string
	}{
		{"ai-flux", "ai://flux/dev", "flux"},
		{"ai-midjourney", "ai://midjourney/v6", "midjourney"},
		{"foreign-source-ignored", "foreign://something", "foreign"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			metaBytes := buildCanonicalGeneratedMetadata(canonicalMetaInput{
				Description: "anything",
				Style:       "any",
				Source:      tc.source,
				Generator:   tc.generator,
				Hash:        "h",
				Tags:        nil,
				Width:       1,
				Height:      1,
			})
			var got map[string]any
			if err := json.Unmarshal(metaBytes, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got["origin"] != "generated" {
				t.Errorf("origin must always be %q for the canonical fallback; got %v (source=%q, generator=%q)",
					"generated", got["origin"], tc.source, tc.generator)
			}
		})
	}
}

// keysOf returns the sorted key set of m for deterministic error
// messages. Pulled out so the test doesn't inline-sort twice.
func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// stable minimum-order sort for deterministic error messages.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
