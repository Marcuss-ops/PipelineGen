// Package acceptance_test — acceptance_recovery_test.go
//
// PR-CLIPINGEST-PIPELINE step 11 (July 2026): category (d).
//
// User spec — "recovery — kill dopo 5 lingue → riparte dalla
// 6 senza ritradurre le prime 5".
//
// Cover:
//   - 5 already-existing target tracks READY+keymatching → 6th run
//     produces 5 Skipped + 1 Created.
//   - Restart with fresh Materializer against same persisted state
//     preserves the work-already-done invariant.
package acceptance_test

import (
	"context"
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
)

// concatInit wraps a per-call language list with the required
// "__init__" sentinel that satisfies texttracks.ResolverConfig.Validate().
// Materialize() at runtime replaces the placeholder with a fresh copy
// of the per-call override (production copy-replaces behaviour
// via `append([]string{}, targetLanguagesOverride...)`).
func concatInit(langs []string) []string {
	out := make([]string, 0, len(langs)+1)
	out = append(out, "__init__")
	out = append(out, langs...)
	return out
}

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
	if err := repo.UpsertBatch(ctx, []detail.TextTrack{
		newSourceTrack(assetID, srcLang, string(detail.TextTrackTranscript), src),
	}); err != nil {
		t.Fatalf("seed source: %v", err)
	}

	first5 := []string{"it", "es", "fr", "de", "pt-BR"}
	m1, err := texttracks.NewMaterializer(repo, tr, ob,
		texttracks.ResolverConfig{
			SourceLanguage:          "en",
			OverrideTargetLanguages: concatInit(first5),
			TranslationModel:        "stub-model", ModelVersion: "v1", PromptVersion: "p1",
		}, zap.NewNop())
	if err != nil {
		t.Fatalf("NewMaterializer(1st): %v", err)
	}
	rep1, err := m1.Materialize(ctx, assetID, srcLang, srcHash, detail.TextTrackTranscript, first5)
	if err != nil {
		t.Fatalf("Materialize(1st): %v", err)
	}
	if len(rep1.CreatedLanguages) != len(first5) {
		t.Fatalf("1st CreatedLanguages = %d, want %d", len(rep1.CreatedLanguages), len(first5))
	}
	callsAfterPhase1 := tr.CallCount()

	all6 := append(first5, "ru")
	m2, err := texttracks.NewMaterializer(repo, tr, ob,
		texttracks.ResolverConfig{
			SourceLanguage:          "en",
			OverrideTargetLanguages: concatInit(all6),
			TranslationModel:        "stub-model", ModelVersion: "v1", PromptVersion: "p1",
		}, zap.NewNop())
	if err != nil {
		t.Fatalf("NewMaterializer(2nd): %v", err)
	}
	rep2, err := m2.Materialize(ctx, assetID, srcLang, srcHash, detail.TextTrackTranscript, all6)
	if err != nil {
		t.Fatalf("Materialize(2nd): %v", err)
	}
	if len(rep2.SkippedLanguages) != len(first5) {
		t.Errorf("after resume, SkippedLanguages = %d, want %d",
			len(rep2.SkippedLanguages), len(first5))
	}
	if len(rep2.CreatedLanguages) != 1 || rep2.CreatedLanguages[0] != "ru" {
		t.Errorf("after resume, CreatedLanguages = %v, want [ru]", rep2.CreatedLanguages)
	}
	if tr.CallCount() != callsAfterPhase1+1 {
		t.Errorf("translator called %d times; want %d",
			tr.CallCount(), callsAfterPhase1+1)
	}
}

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
	if err := repo.UpsertBatch(ctx, []detail.TextTrack{
		newSourceTrack(assetID, srcLang, string(detail.TextTrackTranscript), src),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	m, err := texttracks.NewMaterializer(repo, tr, ob,
		texttracks.ResolverConfig{
			SourceLanguage:          "en",
			OverrideTargetLanguages: concatInit([]string{"it", "es", "fr", "de", "pt-BR", "ru"}),
			TranslationModel:        "stub-model", ModelVersion: "v1", PromptVersion: "p1",
		}, zap.NewNop())
	if err != nil {
		t.Fatalf("NewMaterializer: %v", err)
	}
	rep, err := m.Materialize(ctx, assetID, srcLang, srcHash, detail.TextTrackTranscript,
		[]string{"it", "es", "fr", "de", "pt-BR", "ru"})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if len(rep.SkippedLanguages) != 0 {
		t.Errorf("SkippedLanguages = %v, want []", rep.SkippedLanguages)
	}
	if len(rep.CreatedLanguages) != 6 {
		t.Errorf("CreatedLanguages count = %d, want 6", len(rep.CreatedLanguages))
	}
}

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
	if err := repo.UpsertBatch(ctx, []detail.TextTrack{
		newSourceTrack(assetID, srcLang, string(detail.TextTrackTranscript), src),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	m, err := texttracks.NewMaterializer(repo, tr, ob,
		texttracks.ResolverConfig{
			SourceLanguage:          "en",
			OverrideTargetLanguages: concatInit([]string{"it", "es", "fr"}),
			TranslationModel:        "stub-model", ModelVersion: "v1", PromptVersion: "p1",
		}, zap.NewNop())
	if err != nil {
		t.Fatalf("NewMaterializer: %v", err)
	}
	if _, err := m.Materialize(ctx, assetID, srcLang, srcHash, detail.TextTrackTranscript, []string{"it", "es", "fr"}); err != nil {
		t.Fatalf("Materialize(1st): %v", err)
	}
	callsAfter1 := tr.CallCount()
	if _, err := m.Materialize(ctx, assetID, srcLang, srcHash, detail.TextTrackTranscript, []string{"it", "es", "fr"}); err != nil {
		t.Fatalf("Materialize(2nd): %v", err)
	}
	if tr.CallCount() != callsAfter1 {
		t.Errorf("translator called on 2nd run: %d -> %d", callsAfter1, tr.CallCount())
	}
}
