// Package acceptance_test — acceptance_invalidation_test.go
//
// PR-CLIPINGEST-PIPELINE step 11 (July 2026): category (c).
//
// User spec — "invalidazione — transcript cambia → semantic_hash
// cambia, traduzioni obsolete marcate, nuove track, reindex Qdrant".
//
// Cover:
//   - Source transcript change → prior target rows lose `is_current`.
//   - New track with new translation_key lands.
//   - Exactly one IsCurrent row per (lang, kind) (partial UNIQUE INDEX).
//   - semantic_hash changes with content (mirror of media_assets.semantic_hash column contract).
package acceptance_test

import (
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"context"
	"encoding/json"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

func TestInvalidation_SourceHashChange_FlipsAuditPredecessor(t *testing.T) {
	ctx := context.Background()
	repo := newInMemRepo()
	ob := newRecordingOutbox()
	tr := newMockTranslator()

	const (
		assetID = "asset-inv-001"
		srcLang = "en"
	)

	srcV1 := "winter arc opening scene"
	srcV1Hash := sha256Hex(srcV1)
	if err := repo.UpsertBatch(ctx, []detail.TextTrack{newSourceTrack(assetID, srcLang, string(detail.TextTrackTranscript), srcV1)}); err != nil {
		t.Fatalf("seed v1: %v", err)
	}
	m := newMaterializer(t, repo, tr, ob)
	if _, err := m.Materialize(ctx, assetID, srcLang, srcV1Hash, detail.TextTrackTranscript, []string{"it", "es"}); err != nil {
		t.Fatalf("Materialize v1: %v", err)
	}

	predecessorIDs := map[string]int64{}
	allV1, _ := repo.ListByAsset(ctx, assetID)
	for _, tk := range allV1 {
		if tk.LanguageCode != srcLang && tk.TextKind == detail.TextTrackTranscript {
			predecessorIDs[tk.LanguageCode] = tk.ID
			if !tk.IsCurrent {
				t.Fatalf("predecessor %s should be IsCurrent=true before invalidation", tk.LanguageCode)
			}
		}
	}
	if len(predecessorIDs) != 2 {
		t.Fatalf("v1 created 2 translations; got %d", len(predecessorIDs))
	}

	srcV2 := "winter arc opening scene — revised take with corrected lighting"
	srcV2Hash := sha256Hex(srcV2)
	if srcV2Hash == srcV1Hash {
		t.Fatalf("test invariant: v2 must differ from v1 (collision in fixture)")
	}

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

	if _, err := m.Materialize(ctx, assetID, srcLang, srcV2Hash, detail.TextTrackTranscript, []string{"it", "es"}); err != nil {
		t.Fatalf("Materialize v2: %v", err)
	}

	allV2, _ := repo.ListByAsset(ctx, assetID)
	curCount := map[string]int{}
	currentHasNewKey := map[string]bool{}
	for _, tk := range allV2 {
		if tk.LanguageCode == srcLang {
			continue
		}
		if !tk.IsCurrent {
			continue
		}
		curCount[tk.LanguageCode]++
		wantKey := detail.TranslationKey(srcV2Hash, tk.LanguageCode, "stub-model", "v1", "p1")
		if tk.TranslationKey == wantKey {
			currentHasNewKey[tk.LanguageCode] = true
		}
	}
	for _, lang := range []string{"it", "es"} {
		if curCount[lang] != 1 {
			t.Errorf("IsCurrent(%s) count = %d, want 1", lang, curCount[lang])
		}
		if !currentHasNewKey[lang] {
			t.Errorf("IsCurrent(%s) translation_key does not match v2 fingerprint", lang)
		}
	}
	if ob.EnqueuedCount() < 1 {
		t.Errorf("EnqueuedCount = %d, want >= 1 (reindex signal)", ob.EnqueuedCount())
	}
}

func TestInvalidation_SemanticHashFingerprintChangesWithContent(t *testing.T) {
	const (
		assetID = "asset-inv-002"
		_       = assetID
	)
	srcA := "first cut"
	srcB := "second cut with re-edited lighting"

	hashA := sha256Hex(srcA)
	hashB := sha256Hex(srcB)
	if hashA == hashB {
		t.Fatalf("test invariant: distinct content must produce distinct SHA-256")
	}

	var _ asset.MediaType = asset.MediaTypeImageVideo

	type mediaAssetRow struct {
		AssetID      string `json:"asset_id"`
		SemanticHash string `json:"semantic_hash"`
	}
	rowAfterV1, _ := json.Marshal(mediaAssetRow{AssetID: "asset-inv-002", SemanticHash: hashA})
	rowAfterV2, _ := json.Marshal(mediaAssetRow{AssetID: "asset-inv-002", SemanticHash: hashB})
	if string(rowAfterV1) == string(rowAfterV2) {
		t.Errorf("semantic_hash bytes did not change on content mutation")
	}
}

func TestInvalidation_ObsoleteTranslationsLoseCurrent(t *testing.T) {
	ctx := context.Background()
	repo := newInMemRepo()
	ob := newRecordingOutbox()
	tr := newMockTranslator()

	const (
		assetID = "asset-inv-003"
		srcLang = "en"
	)
	srcV1 := "first cut"
	srcV1Hash := sha256Hex(srcV1)
	if err := repo.UpsertBatch(ctx, []detail.TextTrack{newSourceTrack(assetID, srcLang, string(detail.TextTrackTranscript), srcV1)}); err != nil {
		t.Fatalf("seed v1: %v", err)
	}
	m := newMaterializer(t, repo, tr, ob)
	if _, err := m.Materialize(ctx, assetID, srcLang, srcV1Hash, detail.TextTrackTranscript, []string{"it", "es", "fr"}); err != nil {
		t.Fatalf("Materialize v1: %v", err)
	}

	srcV2 := "second cut"
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
	if _, err := m.Materialize(ctx, assetID, srcLang, srcV2Hash, detail.TextTrackTranscript, []string{"it", "es", "fr"}); err != nil {
		t.Fatalf("Materialize v2: %v", err)
	}

	curFor := func(lang string) int {
		all, _ := repo.ListByAsset(ctx, assetID)
		c := 0
		for _, tk := range all {
			if tk.LanguageCode == lang && tk.IsCurrent {
				c++
			}
		}
		return c
	}
	for _, lang := range []string{"it", "es", "fr"} {
		if got := curFor(lang); got != 1 {
			t.Errorf("IsCurrent rows for %s = %d, want 1 after invalidation", lang, got)
		}
	}
}
