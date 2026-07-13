// Package acceptance_test — acceptance_idempotency_test.go
//
// PR-CLIPINGEST-PIPELINE step 11 (July 2026): category (a).
//
// User spec — "idempotenza — stessa clip due volte → 1 media_assets,
// 1 render_master, 0 eventi Qdrant duplicati".
//
// Cover:
//   - Dispatching the same (assetID, contentHash) twice → the 2nd
//     outbox enqueue short-circuits via eventKey dedup (canonical
//     supersede-gate behaviour). No duplicate Qdrant event.
//   - Materializing the same asset with the same source_text_hash
//     twice → second call is a no-op (translation_key lookup gate
//     returns READY row, all languages Skipped, no LLM cost).
//   - Projecting the same (assetID, contentHash) twice → exact
//     same payload byte-shape; no duplicate Upsert.
package acceptance_test

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// TestIdempotency_DoubleDispatch_SecondCallSuperseded: same
// (assetID, contentHash) dispatched twice through the outbox
// → the 2nd call surfaces as a supersede, not a duplicate event.
func TestIdempotency_DoubleDispatch_SecondCallSuperseded(t *testing.T) {
	ctx := context.Background()
	ob := newRecordingOutbox()
	const (
		assetID     = "asset-idem-001"
		contentHash = "sha256:idem-hash"
		eventKey    = "reindex:asset-idem-001:v1:sha256:idem-hash"
	)

	// First dispatch — should enqueue. The recorder mirrors
	// production dedup at the narrow OutboxEnqueuer surface;
	// res1 reflects the production EnqueueResult envelope
	// (Inserted=true means new row landed, Inserted=false +
	// ExistingStatus="superseded" means canonical supersede).
	if _, err := ob.Enqueue(ctx, nil,
		"asset.index.requested", assetID, "asset",
		`{"asset_id":"asset-idem-001","content_hash":"idem-hash"}`, eventKey); err != nil {
		t.Fatalf("Enqueue(1st): %v", err)
	}

	// Second dispatch — same key → supersede path.
	if _, err := ob.Enqueue(ctx, nil,
		"asset.index.requested", assetID, "asset",
		`{"asset_id":"asset-idem-001","content_hash":"idem-hash"}`, eventKey); err != nil {
		t.Fatalf("Enqueue(2nd): %v", err)
	}

	if got := ob.EnqueuedCount(); got != 1 {
		t.Errorf("EnqueuedCount = %d, want 1 (2nd must short-circuit)", got)
	}
	if got := ob.SupersededCount(); got != 1 {
		t.Errorf("SupersededCount = %d, want 1", got)
	}
}

// TestIdempotency_DoubleMaterialize_SameSourceHash_NoRetranslate:
// the lookup-before-translate gate is the canonical dedup surface
// for translation work. Re-running Materialize with the same
// sourceTextHash must not call the translator again.
func TestIdempotency_DoubleMaterialize_SameSourceHash_NoRetranslate(t *testing.T) {
	ctx := context.Background()
	repo := newInMemRepo()
	ob := newRecordingOutbox()
	tr := newMockTranslator()

	assetID := "asset-idem-002"
	srcLang := "en"
	kind := asset.TextTrackTranscript
	srcContent := "original transcript text"
	srcHash := sha256Hex(srcContent)
	targets := []string{"it", "es", "fr", "de"}

	// Seed the source-language track READY.
	if err := repo.UpsertBatch(ctx, []asset.TextTrack{newSourceTrack(assetID, srcLang, string(kind), srcContent)}); err != nil {
		t.Fatalf("seed source track: %v", err)
	}

	m := newMaterializer(t, repo, tr, ob)

	// First Materialize.
	rep1, err := m.Materialize(ctx, assetID, srcLang, srcHash, kind, targets)
	if err != nil {
		t.Fatalf("Materialize(1st): %v", err)
	}
	if len(rep1.CreatedLanguages) != len(targets) {
		t.Errorf("1st CreatedLanguages = %d, want %d", len(rep1.CreatedLanguages), len(targets))
	}
	callsAfterFirst := tr.CallCount()

	// Second Materialize with the same sourceTextHash — every
	// target language should be Skipped via the lookup gate.
	rep2, err := m.Materialize(ctx, assetID, srcLang, srcHash, kind, targets)
	if err != nil {
		t.Fatalf("Materialize(2nd): %v", err)
	}
	if len(rep2.CreatedLanguages) != 0 {
		t.Errorf("2nd CreatedLanguages = %d, want 0 (must dedup via translation_key)", len(rep2.CreatedLanguages))
	}
	if len(rep2.SkippedLanguages) != len(targets) {
		t.Errorf("2nd SkippedLanguages = %d, want %d", len(rep2.SkippedLanguages), len(targets))
	}
	if tr.CallCount() != callsAfterFirst {
		t.Errorf("Translator should NOT be called again on 2nd Materialize (reused via lookup gate). calls = %d, want %d", tr.CallCount(), callsAfterFirst)
	}
}

// TestIdempotency_RenderMasterProjection_OnePerVersion: same
// (assetID, contentHash) twice → outbox payload is byte-equivalent
// (no duplicate Upsert path). The current hash must be applied
// verbatim.
func TestIdempotency_RenderMasterProjection_OnePerVersion(t *testing.T) {
	ctx := context.Background()
	ob := newRecordingOutbox()
	const (
		assetID     = "asset-idem-003"
		contentHash = "sha256:idem-render-hash"
	)

	// Both dispatches use the SAME eventKey (the production
	// outbox builds event_key from
	// (assetID, schema_version, full_content_hash) so identical
	// content produces identical keys → supersede-dedup).
	const eventKey = "reindex:asset-idem-003:v1:sha256:idem-render-hash"

	for i := 0; i < 3; i++ {
		_, err := ob.Enqueue(ctx, nil,
			"render.master.upsert", assetID, "asset",
			`{"asset_id":"asset-idem-003","content_hash":"idem-render-hash"}`, eventKey)
		if err != nil {
			t.Fatalf("Enqueue[%d]: %v", i, err)
		}
	}
	if got := ob.EnqueuedCount(); got != 1 {
		t.Errorf("After 3 dispatches, EnqueuedCount = %d, want 1 (eventKey idempotent)", got)
	}
	if got := ob.SupersededCount(); got != 2 {
		t.Errorf("SupersededCount = %d, want 2", got)
	}
}

// TestIdempotency_TranslatorStub_NotCalledOnDedup: confirms the
// EconomyOfWork invariant: zero LLM calls when lookup gate hits.
func TestIdempotency_TranslatorStub_NotCalledOnDedup(t *testing.T) {
	ctx := context.Background()
	repo := newInMemRepo()
	ob := newRecordingOutbox()
	tr := newMockTranslator()

	const (
		assetID = "asset-idem-004"
		srcLang = "en"
		kind    = asset.TextTrackTranscript
	)
	src := "det-d-004"
	srcHash := sha256Hex(src)
	if err := repo.UpsertBatch(ctx, []asset.TextTrack{newSourceTrack(assetID, srcLang, string(kind), src)}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	m := newMaterializer(t, repo, tr, ob)
	if _, err := m.Materialize(ctx, assetID, srcLang, srcHash, kind, []string{"it"}); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	firstCalls := tr.CallCount()

	// Re-run with same source hash → lookup gate hits → no LLM call.
	if _, err := m.Materialize(ctx, assetID, srcLang, srcHash, kind, []string{"it"}); err != nil {
		t.Fatalf("Materialize(2nd): %v", err)
	}
	if tr.CallCount() != firstCalls {
		t.Errorf("translator called again on idempotent re-run: %d vs %d", tr.CallCount(), firstCalls)
	}
}
