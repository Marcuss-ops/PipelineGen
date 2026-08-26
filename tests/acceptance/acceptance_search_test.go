// Package acceptance_test — acceptance_search_test.go
//
// PR-CLIPINGEST-PIPELINE step 11 (July 2026): category (e).
//
// User spec — "ricerca — stessa scena nelle 10 lingue → stesso
// asset_id".
//
// Cover:
//   - For every translated scene, the canonical media_assets.id
//     remains invariant across all 10 language variants.
//   - Localization is textual (TextContent differs across languages),
//     but identity (AssetID) is content-hash-keyed.
package acceptance_test

import (
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"context"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

func TestSearch_SameSceneAcross10Languages_SameAssetID(t *testing.T) {
	ctx := context.Background()
	repo := newInMemRepo()
	ob := newRecordingOutbox()
	tr := newMockTranslator()

	const (
		assetID = "asset-search-001"
		srcLang = "en"
	)
	src := "narrator walks through a misty forest at dawn"
	srcHash := sha256Hex(src)
	if err := repo.UpsertBatch(ctx, []detail.TextTrack{
		newSourceTrack(assetID, srcLang, string(detail.TextTrackTranscript), src),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	langs := []string{"it", "es", "fr", "de", "pt", "ru", "ja", "zh", "ko", "ar"}
	m := newMaterializer(t, repo, tr, ob)
	if _, err := m.Materialize(ctx, assetID, srcLang, srcHash, detail.TextTrackTranscript, langs); err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	all, _ := repo.ListByAsset(ctx, assetID)
	if len(all) != 1+len(langs) {
		t.Fatalf("expected 1 source + %d langs = %d rows; got %d",
			len(langs), 1+len(langs), len(all))
	}
	for _, tk := range all {
		if tk.AssetID != assetID {
			t.Errorf("track %s/%s/%s has AssetID %q, want %q",
				tk.LanguageCode, tk.TextKind, tk.Provider,
				tk.AssetID, assetID)
		}
	}
}

func TestSearch_LocalizationIsTextualNotIdentity(t *testing.T) {
	ctx := context.Background()
	repo := newInMemRepo()
	ob := newRecordingOutbox()
	tr := newMockTranslator()

	const (
		assetID = "asset-search-002"
		srcLang = "en"
		src     = "the plane descends into the canyon at sunset"
	)
	srcHash := sha256Hex(src)
	if err := repo.UpsertBatch(ctx, []detail.TextTrack{
		newSourceTrack(assetID, srcLang, string(detail.TextTrackTranscript), src),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	langs := []string{"it", "es", "fr", "de", "pt", "ru", "ja", "zh", "ko", "ar"}
	m := newMaterializer(t, repo, tr, ob)
	if _, err := m.Materialize(ctx, assetID, srcLang, srcHash, detail.TextTrackTranscript, langs); err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	all, _ := repo.ListByAsset(ctx, assetID)
	perLangContent := map[string]string{}
	for _, tk := range all {
		if tk.LanguageCode == srcLang {
			continue
		}
		if tk.SourceTextHash != srcHash {
			t.Errorf("track[%s].source_text_hash = %q, want %q (parent-hash-propagation byte-equality invariant violated)",
				tk.LanguageCode, tk.SourceTextHash, srcHash)
		}
		if tk.AssetID != assetID {
			t.Errorf("track[%s].AssetID %q != canonical %q",
				tk.LanguageCode, tk.AssetID, assetID)
		}
		perLangContent[tk.LanguageCode] = tk.TextContent
	}

	for _, l := range langs {
		c, ok := perLangContent[l]
		if !ok {
			t.Errorf("missing track for language %s", l)
			continue
		}
		if c == src {
			t.Errorf("language %s: TextContent equals source (no localization produced)", l)
		}
		if !strings.HasPrefix(c, "[") || !strings.Contains(c, l) {
			t.Errorf("language %s: TextContent %q does not carry the markdown-ish language prefix",
				l, c)
		}
	}
}

func TestSearch_Localization_StaysTextualAcrossRounds(t *testing.T) {
	ctx := context.Background()
	repo := newInMemRepo()
	ob := newRecordingOutbox()
	tr := newMockTranslator()

	const (
		assetID = "asset-search-004"
		srcLang = "en"
	)
	srcV1 := "first cut lighting"
	srcV1Hash := sha256Hex(srcV1)
	if err := repo.UpsertBatch(ctx, []detail.TextTrack{
		newSourceTrack(assetID, srcLang, string(detail.TextTrackTranscript), srcV1),
	}); err != nil {
		t.Fatalf("seed v1: %v", err)
	}
	m := newMaterializer(t, repo, tr, ob)
	if _, err := m.Materialize(ctx, assetID, srcLang, srcV1Hash, detail.TextTrackTranscript, []string{"it", "fr", "de", "ja"}); err != nil {
		t.Fatalf("Materialize v1: %v", err)
	}

	srcV2 := "second cut lighting"
	srcV2Hash := sha256Hex(srcV2)
	if err := repo.UpsertBatch(ctx, []detail.TextTrack{
		func() detail.TextTrack {
			s := newSourceTrack(assetID, srcLang, string(detail.TextTrackTranscript), srcV2)
			s.SourceTextHash = srcV2Hash
			s.TextHash = srcV2Hash
			return s
		}(),
	}); err != nil {
		t.Fatalf("seed v2: %v", err)
	}
	if _, err := m.Materialize(ctx, assetID, srcLang, srcV2Hash, detail.TextTrackTranscript, []string{"it", "fr", "de", "ja"}); err != nil {
		t.Fatalf("Materialize v2: %v", err)
	}

	all, _ := repo.ListByAsset(ctx, assetID)
	for _, tk := range all {
		if tk.AssetID != assetID {
			t.Errorf("track %s has AssetID %q, want %q (asset_id invariant across source revisions)",
				tk.LanguageCode, tk.AssetID, assetID)
		}
	}

	var _ asset.MediaType = asset.MediaTypeImageVideo
}
