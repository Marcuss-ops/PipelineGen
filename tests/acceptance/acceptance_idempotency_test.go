// Package acceptance_test — acceptance_idempotency_test.go
//
// PR-CLIPINGEST-PIPELINE step 11 (July 2026): category (a).
//
// User spec — "idempotenza — stessa clip due volte → 1 media_assets,
// 1 render_master, 0 eventi Qdrant duplicati".
//
// Cover:
//   - Dispatching the same (assetID, contentHash) twice → the 2nd
//     outbox enqueue short-circuits via eventKey dedup.
//   - Materializing the same asset with the same source_text_hash
//     twice → second call is a no-op (translation_key lookup gate).
//   - Same contentHash rendered twice → exact same projection.
package acceptance_test

import (
	"context"
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"testing"
)

func TestIdempotency_DoubleDispatch_SecondCallSuperseded(t *testing.T) {
	ctx := context.Background()
	ob := newRecordingOutbox()
	const (
		assetID     = "asset-idem-001"
		contentHash = "sha256:idem-hash"
		eventKey    = "reindex:asset-idem-001:v1:sha256:idem-hash"
	)

	if _, err := ob.Enqueue(ctx, nil,
		"asset.index.requested", assetID, "asset",
		`{"asset_id":"asset-idem-001","content_hash":"idem-hash"}`, eventKey); err != nil {
		t.Fatalf("Enqueue(1st): %v", err)
	}
	if _, err := ob.Enqueue(ctx, nil,
		"asset.index.requested", assetID, "asset",
		`{"asset_id":"asset-idem-001","content_hash":"idem-hash"}`, eventKey); err != nil {
		t.Fatalf("Enqueue(2nd): %v", err)
	}

	if got := ob.EnqueuedCount(); got != 1 {
		t.Errorf("EnqueuedCount = %d, want 1", got)
	}
	if got := ob.SupersededCount(); got != 1 {
		t.Errorf("SupersededCount = %d, want 1", got)
	}
}

func TestIdempotency_DoubleMaterialize_SameSourceHash_NoRetranslate(t *testing.T) {
	ctx := context.Background()
	repo := newInMemRepo()
	ob := newRecordingOutbox()
	tr := newMockTranslator()

	assetID := "asset-idem-002"
	srcLang := "en"
	kind := detail.TextTrackTranscript
	srcContent := "original transcript text"
	srcHash := sha256Hex(srcContent)
	targets := []string{"it", "es", "fr", "de"}

	if err := repo.UpsertBatch(ctx, []detail.TextTrack{newSourceTrack(assetID, srcLang, string(kind), srcContent)}); err != nil {
		t.Fatalf("seed source track: %v", err)
	}
	m := newMaterializer(t, repo, tr, ob)

	rep1, err := m.Materialize(ctx, assetID, srcLang, srcHash, kind, targets)
	if err != nil {
		t.Fatalf("Materialize(1st): %v", err)
	}
	if len(rep1.CreatedLanguages) != len(targets) {
		t.Errorf("1st CreatedLanguages = %d, want %d", len(rep1.CreatedLanguages), len(targets))
	}
	callsAfterFirst := tr.CallCount()

	rep2, err := m.Materialize(ctx, assetID, srcLang, srcHash, kind, targets)
	if err != nil {
		t.Fatalf("Materialize(2nd): %v", err)
	}
	if len(rep2.CreatedLanguages) != 0 {
		t.Errorf("2nd CreatedLanguages = %d, want 0", len(rep2.CreatedLanguages))
	}
	if len(rep2.SkippedLanguages) != len(targets) {
		t.Errorf("2nd SkippedLanguages = %d, want %d", len(rep2.SkippedLanguages), len(targets))
	}
	if tr.CallCount() != callsAfterFirst {
		t.Errorf("translator called again on 2nd Materialize: got %d, want %d",
			tr.CallCount(), callsAfterFirst)
	}
}

func TestIdempotency_RenderMasterProjection_OnePerVersion(t *testing.T) {
	ctx := context.Background()
	ob := newRecordingOutbox()
	const (
		assetID     = "asset-idem-003"
		contentHash = "sha256:idem-render-hash"
		eventKey    = "reindex:asset-idem-003:v1:sha256:idem-render-hash"
	)

	for i := 0; i < 3; i++ {
		_, err := ob.Enqueue(ctx, nil,
			"render.master.upsert", assetID, "asset",
			`{"asset_id":"asset-idem-003","content_hash":"idem-render-hash"}`, eventKey)
		if err != nil {
			t.Fatalf("Enqueue[%d]: %v", i, err)
		}
	}
	if got := ob.EnqueuedCount(); got != 1 {
		t.Errorf("EnqueuedCount = %d, want 1 (eventKey idempotent)", got)
	}
	if got := ob.SupersededCount(); got != 2 {
		t.Errorf("SupersededCount = %d, want 2", got)
	}
}

func TestIdempotency_TranslatorStub_NotCalledOnDedup(t *testing.T) {
	ctx := context.Background()
	repo := newInMemRepo()
	ob := newRecordingOutbox()
	tr := newMockTranslator()

	const (
		assetID = "asset-idem-004"
		srcLang = "en"
		kind    = detail.TextTrackTranscript
	)
	src := "det-d-004"
	srcHash := sha256Hex(src)
	if err := repo.UpsertBatch(ctx, []detail.TextTrack{newSourceTrack(assetID, srcLang, string(kind), src)}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	m := newMaterializer(t, repo, tr, ob)
	if _, err := m.Materialize(ctx, assetID, srcLang, srcHash, kind, []string{"it"}); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	firstCalls := tr.CallCount()

	if _, err := m.Materialize(ctx, assetID, srcLang, srcHash, kind, []string{"it"}); err != nil {
		t.Fatalf("Materialize(2nd): %v", err)
	}
	if tr.CallCount() != firstCalls {
		t.Errorf("translator called again on idempotent re-run: %d vs %d",
			tr.CallCount(), firstCalls)
	}
}
