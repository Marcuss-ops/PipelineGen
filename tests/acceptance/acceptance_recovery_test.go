// Package acceptance_test — acceptance_recovery_test.go
//
// PR-CLIPINGEST-PIPELINE step 11 (July 2026): category (d).
//
// User spec — "recovery — kill dopo 5 lingue → riparte dalla
// 6 senza ritradurre le prime 5".
//
// Cover:
//   - After 5 target translations land READY+IsCurrent=1, the
//     6th invocation of Materialize with the same source hash
//     must produce ZERO LLM calls for the first 5
//     (lookup-before-translate gate hits), and exactly one
//     new translation for the 6th.
//   - The Materializer is RESTART-idempotent: simulate a kill
//     after 5 by destroying the previous Materializer and
//     building a fresh one against the same persisted repo +
//     translator; the fresh materializer must pick up exactly
//     from where the previous run stopped.
package acceptance_test

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// TestRecovery_PreExistingFive_NoRetranslationOnResume: seed 5
// target tracks READY+IsCurrent+KeyMatching the lookup gate.
// Materialize the same 6 languages → first 5 Skipped, 6th Created.
// Translator is called exactly ONCE (one new LLM cost).
func TestRecovery_PreExistingFive_NoRetranslationOnResume(t *testing.T) {
	ctx := context.Background()
	repo := newInMemRepo()
	ob := newRecordingOutbox()
	tr := newMockTranslator()

	const (
		assetID = "asset-rec-001"
		srcLang = "en"
	)
	src := "the source content for recovery test"
	srcHash := sha256Hex(src)
	if err := repo.UpsertBatch(ctx, []asset.TextTrack{
		newSourceTrack(assetID, srcLang, string(asset.TextTrackTranscript), src),
	}); err != nil {
		t.Fatalf("seed source: %v", err)
	}

	// PHASE 1: simulate the first run completing 5 of 6 langs,
	// then "killed" before reaching lang-6.
	first5 := []string{"it", "es", "fr", "de", "pt"}
	m1, err := texttracks.NewMaterializer(repo, tr, ob,
		texttracks.ResolverConfig{
			SourceLanguage:          "en",
			OverrideTargetLanguages: first5,
			TranslationModel:        "stub-model", ModelVersion: "v1", PromptVersion: "p1",
		}, zap.NewNop())
	if err != nil {
		t.Fatalf("NewMaterializer(1st): %v", err)
	}
	rep1, err := m1.Materialize(ctx, assetID, srcLang, srcHash, asset.TextTrackTranscript, first5)
	if err != nil {
		t.Fatalf("Materialize(1st): %v", err)
	}
	if len(rep1.CreatedLanguages) != len(first5) {
		t.Fatalf("1st CreatedLanguages = %d, want %d", len(rep1.CreatedLanguages), len(first5))
	}
	callsAfterPhase1 := tr.CallCount()

	// PHASE 2: simulate "process killed" — discard materializer,
	// keep repo + translator. Build a fresh materializer that
	// sees the previously persisted state. Materialize the
	// COMPLETE 6-language list — first 5 must Skipped, lang-6
	// must Created. The translator gets called ONCE for the 6th.
	all6 := append(first5, "ru")
	m2, err := texttracks.NewMaterializer(repo, tr, ob,
		texttracks.ResolverConfig{
			SourceLanguage:          "en",
			OverrideTargetLanguages: all6,
			TranslationModel:        "stub-model", ModelVersion: "v1", PromptVersion: "p1",
		}, zap.NewNop())
	if err != nil {
		t.Fatalf("NewMaterializer(2nd): %v", err)
	}
	rep2, err := m2.Materialize(ctx, assetID, srcLang, srcHash, asset.TextTrackTranscript, all6)
	if err != nil {
		t.Fatalf("Materialize(2nd): %v", err)
	}
	if len(rep2.SkippedLanguages) != len(first5) {
		t.Errorf("after resume, SkippedLanguages = %d, want %d (lookup gate hits)",
			len(rep2.SkippedLanguages), len(first5))
	}
	if len(rep2.CreatedLanguages) != 1 || rep2.CreatedLanguages[0] != "ru" {
		t.Errorf("after resume, CreatedLanguages = %v, want [ru]", rep2.CreatedLanguages)
	}
	if tr.CallCount() != callsAfterPhase1+1 {
		t.Errorf("translator called %d times; want %d (5 phase1 + 1 phase2 lang-6)",
			tr.CallCount(), callsAfterPhase1+1)
	}
}

// TestRecovery_FirstSix_AllCreated_NoSkipped: counter-case —
// if the persisted state has ZERO target rows, the materializer
// MUST translate all 6 from scratch (no spurious "skipped").
func TestRecovery_FirstSix_AllCreated_NoSkipped(t *testing.T) {
	ctx := context.Background()
	repo := newInMemRepo()
	ob := newRecordingOutbox()
	tr := newMockTranslator()

	const (
		assetID = "asset-rec-002"
		srcLang = "en"
	)
	src := "fresh asset — no translations yet"
	srcHash := sha256Hex(src)
	if err := repo.UpsertBatch(ctx, []asset.TextTrack{
		newSourceTrack(assetID, srcLang, string(asset.TextTrackTranscript), src),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	m, err := texttracks.NewMaterializer(repo, tr, ob,
		texttracks.ResolverConfig{
			SourceLanguage:          "en",
			OverrideTargetLanguages: []string{"it", "es", "fr", "de", "pt", "ru"},
			TranslationModel:        "stub-model", ModelVersion: "v1", PromptVersion: "p1",
		}, zap.NewNop())
	if err != nil {
		t.Fatalf("NewMaterializer: %v", err)
	}
	rep, err := m.Materialize(ctx, assetID, srcLang, srcHash, asset.TextTrackTranscript,
		[]string{"it", "es", "fr", "de", "pt", "ru"})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if len(rep.SkippedLanguages) != 0 {
		t.Errorf("SkippedLanguages = %v, want [] (no translations pre-existing)", rep.SkippedLanguages)
	}
	if len(rep.CreatedLanguages) != 6 {
		t.Errorf("CreatedLanguages count = %d, want 6", len(rep.CreatedLanguages))
	}
}

// TestRecovery_NoDoubleMaterializeOnRestart: the SECOND call
// with the same source hash produces ZERO LLM cost (lookup gate
// hits every language). This is the canonical "no work on
// re-do" surface.
func TestRecovery_NoDoubleMaterializeOnRestart(t *testing.T) {
	ctx := context.Background()
	repo := newInMemRepo()
	ob := newRecordingOutbox()
	tr := newMockTranslator()

	const (
		assetID = "asset-rec-003"
		srcLang = "en"
	)
	src := "deterministic source 003"
	srcHash := sha256Hex(src)
	if err := repo.UpsertBatch(ctx, []asset.TextTrack{
		newSourceTrack(assetID, srcLang, string(asset.TextTrackTranscript), src),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	m, err := texttracks.NewMaterializer(repo, tr, ob,
		texttracks.ResolverConfig{
			SourceLanguage:          "en",
			OverrideTargetLanguages: []string{"it", "es", "fr"},
			TranslationModel:        "stub-model", ModelVersion: "v1", PromptVersion: "p1",
		}, zap.NewNop())
	if err != nil {
		t.Fatalf("NewMaterializer: %v", err)
	}
	if _, err := m.Materialize(ctx, assetID, srcLang, srcHash, asset.TextTrackTranscript, []string{"it", "es", "fr"}); err != nil {
		t.Fatalf("Materialize(1st): %v", err)
	}
	callsAfter1 := tr.CallCount()
	if _, err := m.Materialize(ctx, assetID, srcLang, srcHash, asset.TextTrackTranscript, []string{"it", "es", "fr"}); err != nil {
		t.Fatalf("Materialize(2nd): %v", err)
	}
	if tr.CallCount() != callsAfter1 {
		t.Errorf("translator called on 2nd run when all languages are READY+keymatch: %d -> %d",
			callsAfter1, tr.CallCount())
	}
}
