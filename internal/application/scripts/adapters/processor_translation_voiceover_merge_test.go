// Package adapters — processor_translation_voiceover_merge_test.go:
// hermetic TDD regression guard for the TranslationProcessor →
// mergePostProcessResult → VoiceoverProcessor merge write-back
// chain (Bug 2 end-to-end lock).
//
// The existing TestPipeline_TranslationFeedsVoiceoverProcessor in
// processor_translation_pipeline_test.go tests the same chain
// through the PostProcessorRegistry.Run surface (pipeline-level).
// This test is LOWER-LEVEL: it exercises the three stages explicitly
// (Process → mergePostProcessResult → Process) without going through
// the registry's Run loop, so a regression in mergePostProcessResult's
// write-back logic is isolated from the registry's per-processor
// dispatch, freeze, and freeze-gate concerns.
//
// Reuses stubs from processor_translation_pipeline_test.go (same
// package): pipelineTranslatorStub, pipelineTransUCStub,
// pipelineClassifyStub, pipelineVOStub. No duplication.
package adapters

import (
	"context"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// TestTranslationMergeWriteback_VoiceoverReceivesItalianText
// is the canonical hermetic regression guard for Bug 2:
//
//	TranslationProcessor.Process()
//	  ↓ PostProcessResult{TranslatedText, TranslatedSpecScene}
//	mergePostProcessResult(dst, src, &currentInput)
//	  ↓ currentInput.Text ← src.TranslatedText
//	  ↓ currentInput.SpecScene ← src.TranslatedSpecScene
//	VoiceoverProcessor.Process(ctx, plan, currentInput)
//	  ↓ reads currentInput.Text (must be Italian)
//	  ↓ reads currentInput.SpecScene.Scenes[].Text (must be Italian)
//
// Before the mergePostProcessResult write-back fix, VoiceoverProcessor
// received the original English text because ProcessInput is passed by
// VALUE to each processor — in-place mutations inside
// TranslationProcessor.Process() are lost when the caller returns.
// Only mergePostProcessResult writes the translated surface back to
// the shared currentInput so downstream processors see it.
func TestTranslationMergeWriteback_VoiceoverReceivesItalianText(t *testing.T) {
	// ── Arrange: build processors with canonical stubs ──
	voStub := &pipelineVOStub{}
	voProc := NewVoiceoverProcessor(voStub, zap.NewNop())

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:          "merge-writeback-test",
		Language:    "en",
		TranslateTo: "it",
		Title:       "Boxing Story",
		Postprocessors: []string{
			string(ProcessorTranslation),
			string(ProcessorVoiceover),
		},
	}

	// English input with 2 scenes — the source text that
	// TranslationProcessor will translate to Italian.
	currentInput := ProcessInput{
		Text: "The fighter walks into the arena.",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{
				{
					ID:    "scene-0",
					Index: 0,
					Kind:  "intro",
					Text:  "Welcome to the arena.",
				},
				{
					ID:    "scene-1",
					Index: 1,
					Kind:  "clip",
					Text:  "The crowd roars.",
				},
			},
		},
	}

	// ── Act step 1: TranslationProcessor ──
	// Use pipelineTranslatorStub (prefixes [it]) and
	// pipelineTransUCStub (delegates to translator per field).
	txProc := NewTranslationProcessor(
		pipelineTranslatorStub{},
		nil, // metrics → noop
		pipelineTransUCStub{},
		pipelineClassifyStub{},
		zap.NewNop(),
	)

	txResult, txErr := txProc.Process(context.Background(), plan, currentInput)
	if txErr != nil {
		t.Fatalf("TranslationProcessor.Process() returned error: %v", txErr)
	}
	if txResult == nil {
		t.Fatal("TranslationProcessor.Process() returned nil result")
	}
	// Sanity: TranslationProcessor succeeded and produced Italian text.
	if txResult.TranslatedText == "" {
		t.Fatal("TranslationProcessor.TranslatedText is empty — no translated content to merge")
	}
	if len(txResult.TranslatedSpecScene.Scenes) != 2 {
		t.Fatalf("TranslationProcessor.TranslatedSpecScene.Scenes = %d, want 2", len(txResult.TranslatedSpecScene.Scenes))
	}

	// ── Act step 2: mergePostProcessResult writes back ──
	// This is the load-bearing seam: before the fix,
	// currentInput.Text was still English after this call.
	mergePostProcessResult(&PipelineResult{}, txResult, &currentInput)

	// ── Act step 3: VoiceoverProcessor reads merged input ──
	voResult, voErr := voProc.Process(context.Background(), plan, currentInput)
	if voErr != nil {
		t.Fatalf("VoiceoverProcessor.Process() returned error: %v", voErr)
	}

	// ── Assert: VoiceoverProcessor received Italian text ──

	// Invariant 1: mergePostProcessResult wrote translated text to
	// currentInput.Text — the model-level text must be Italian.
	if !strings.Contains(currentInput.Text, "[it]") {
		t.Errorf(
			"mergePostProcessResult did NOT write TranslatedText to currentInput.Text\n"+
				"  got:  %q\n"+
				"  want: contains [it] (Italian marker from pipelineTranslatorStub)",
			currentInput.Text,
		)
	}

	// Invariant 2: mergePostProcessResult wrote translated SpecScene
	// to currentInput.SpecScene — every scene must be Italian.
	if len(currentInput.SpecScene.Scenes) != 2 {
		t.Fatalf("currentInput.SpecScene.Scenes = %d, want 2 after merge", len(currentInput.SpecScene.Scenes))
	}
	itCount := 0
	for _, sc := range currentInput.SpecScene.Scenes {
		if strings.Contains(sc.Text, "[it]") {
			itCount++
		}
	}
	if itCount != 2 {
		t.Errorf(
			"mergePostProcessResult wrote Italian to only %d/2 scene texts; all must be Italian\n"+
				"  scene-0: %q\n"+
				"  scene-1: %q",
			itCount,
			currentInput.SpecScene.Scenes[0].Text,
			currentInput.SpecScene.Scenes[1].Text,
		)
	}

	// Invariant 3: VoiceoverProcessor captured Italian text
	// (not English) for each scene. RunVoiceoverSceneFanout uses
	// concurrent.ParallelMap, so captured texts may arrive in any
	// order — verify set membership.
	voStub.mu.Lock()
	captured := append([]string(nil), voStub.capturedTexts...)
	voStub.mu.Unlock()

	if len(captured) != 2 {
		t.Fatalf("VoiceoverService received %d calls, want 2", len(captured))
	}
	for i, text := range captured {
		if !strings.Contains(text, "[it]") {
			t.Errorf(
				"VoiceoverService scene %d received UNTRANSLATED text %q — "+
					"mergePostProcessResult write-back is broken",
				i, text,
			)
		}
	}

	// Invariant 4: both scene texts are present in the captured set
	// (order-independent, mirroring the pipeline-level test pattern).
	joined := strings.Join(captured, "|||")
	if !strings.Contains(joined, "Welcome to the arena.") {
		t.Errorf(
			"VoiceoverService did not receive scene-0 text; captured=%v",
			captured,
		)
	}
	if !strings.Contains(joined, "The crowd roars.") {
		t.Errorf(
			"VoiceoverService did not receive scene-1 text; captured=%v",
			captured,
		)
	}

	// Invariant 5: voiceover outcomes are "completed" (canonical
	// pipeline contract — the stub returns success).
	if len(voResult.Voiceovers) != 2 {
		t.Fatalf("VoiceoverProcessor.Voiceovers = %d, want 2", len(voResult.Voiceovers))
	}
	for _, v := range voResult.Voiceovers {
		if v.Status != "completed" {
			t.Errorf("Voiceover scene %d status = %q, want %q", v.SceneIndex, v.Status, "completed")
		}
		if v.Link == "" {
			t.Errorf("Voiceover scene %d has empty Link", v.SceneIndex)
		}
	}
}

// TestTranslationMergeWriteback_NoTranslationPreservesEnglish is the
// NEGATIVE case: when TranslationProcessor is NOT in the pipeline
// (mergePostProcessResult never called with translated content),
// VoiceoverProcessor MUST receive the original English text.
//
// This test guards against a false-positive where the merge write-back
// leaks translated content into processors that should see the original
// language (e.g. when the plan does not request translation).
func TestTranslationMergeWriteback_NoTranslationPreservesEnglish(t *testing.T) {
	voStub := &pipelineVOStub{}
	voProc := NewVoiceoverProcessor(voStub, zap.NewNop())

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:       "no-translation-test",
		Language: "en",
		// No TranslateTo — no translation requested.
	}

	// English input — no mergePostProcessResult with translated content.
	input := ProcessInput{
		Text: "The fighter walks into the arena.",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{
				{
					ID:    "scene-0",
					Index: 0,
					Text:  "Welcome to the arena.",
				},
			},
		},
	}

	voResult, err := voProc.Process(context.Background(), plan, input)
	if err != nil {
		t.Fatalf("VoiceoverProcessor.Process() returned error: %v", err)
	}

	// Assert: VoiceoverProcessor received English text (no [it] marker).
	voStub.mu.Lock()
	captured := append([]string(nil), voStub.capturedTexts...)
	voStub.mu.Unlock()

	if len(captured) != 1 {
		t.Fatalf("VoiceoverService received %d calls, want 1", len(captured))
	}
	if strings.Contains(captured[0], "[it]") {
		t.Errorf(
			"VoiceoverService received Italian text %q when no translation was requested — "+
				"mergePostProcessResult write-back leaked into an untranslated pipeline",
			captured[0],
		)
	}
	if !strings.Contains(captured[0], "Welcome to the arena.") {
		t.Errorf(
			"VoiceoverService did not receive original English text; got %q",
			captured[0],
		)
	}

	// Voiceover outcome is "completed" (stub returns success).
	if len(voResult.Voiceovers) != 1 {
		t.Fatalf("VoiceoverProcessor.Voiceovers = %d, want 1", len(voResult.Voiceovers))
	}
	if voResult.Voiceovers[0].Status != "completed" {
		t.Errorf("Voiceover scene status = %q, want %q", voResult.Voiceovers[0].Status, "completed")
	}
}

// TestTranslationMergeWriteback_PartialTranslationFailurePreservesEnglish
// tests the edge case where TranslationProcessor FAILS (translator
// returns an error), so the result has Changed=false and empty
// TranslatedText. The production Run() loop calls
// mergePostProcessResult UNCONDITIONALLY (even on error) to preserve
// partial results, but the merge is a no-op when TranslatedText is
// empty and TranslatedSpecScene.Scenes is nil — so VoiceoverProcessor
// MUST still receive the original English text.
//
// This guards against a scenario where a future refactor makes
// mergePostProcessResult overwrite currentInput with empty content
// on failure (e.g. zeroing out currentInput.Text when
// src.TranslatedText is "").
func TestTranslationMergeWriteback_PartialTranslationFailurePreservesEnglish(t *testing.T) {
	voStub := &pipelineVOStub{}
	voProc := NewVoiceoverProcessor(voStub, zap.NewNop())

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:          "tx-failure-test",
		Language:    "en",
		TranslateTo: "it",
	}

	// Use a stub that fails translation (simulates translator error).
	failingUC := failingTranslationUseCase{}
	txProc := NewTranslationProcessor(
		pipelineTranslatorStub{}, // translator itself succeeds
		nil,
		failingUC, // but useCase fails
		pipelineClassifyStub{},
		zap.NewNop(),
	)

	currentInput := ProcessInput{
		Text: "The fighter walks into the arena.",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{
				{
					ID:    "scene-0",
					Index: 0,
					Text:  "Welcome to the arena.",
				},
			},
		},
	}

	// TranslationProcessor should return an error (translation failed).
	txResult, txErr := txProc.Process(context.Background(), plan, currentInput)
	if txErr == nil {
		t.Fatal("Expected TranslationProcessor to return error when useCase fails, got nil")
	}
	if txResult.Changed {
		t.Error("Expected Changed=false on translation failure")
	}

	// Production Run() calls mergePostProcessResult unconditionally.
	// On failure, the result has TranslatedText="" and nil Scenes,
	// so the merge is a no-op on currentInput.Text and
	// currentInput.SpecScene (the if-branches guard against empty).
	mergePostProcessResult(&PipelineResult{}, txResult, &currentInput)

	// Act: VoiceoverProcessor receives the merged input.
	// Because translation failed, the voiceover must be skipped.
	voResult, err := voProc.Process(context.Background(), plan, currentInput)
	if err != nil {
		t.Fatalf("VoiceoverProcessor.Process() returned error: %v", err)
	}

	voStub.mu.Lock()
	captured := append([]string(nil), voStub.capturedTexts...)
	voStub.mu.Unlock()

	// New behavior: voiceover must be skipped when requested translation is not completed
	if len(captured) != 0 {
		t.Fatalf("VoiceoverService received %d calls, want 0 (voiceover should be skipped)", len(captured))
	}
	if len(voResult.Voiceovers) != 1 {
		t.Fatalf("VoiceoverProcessor.Voiceovers = %d, want 1", len(voResult.Voiceovers))
	}
	if voResult.Voiceovers[0].Status != "skipped" {
		t.Errorf("Expected status to be 'skipped', got %q", voResult.Voiceovers[0].Status)
	}
	if len(voResult.Warnings) == 0 {
		t.Error("Expected warnings to be present on skip")
	}
}

// ── Helpers ──────────────────────────────────────────────────────────

// Compile-time pin: failingTranslationUseCase must satisfy ports.TranslationUseCase.
var _ ports.TranslationUseCase = failingTranslationUseCase{}

// failingTranslationUseCase is a ports.TranslationUseCase stub that
// always returns an error. Used to simulate translator failure in
// the partial-translation-failure test case.
type failingTranslationUseCase struct{}

func (failingTranslationUseCase) TranslateScriptSpec(
	_ context.Context,
	_ *scriptpkg.ModelScriptOutputV1,
	_ *scriptpkg.ClipEvidence,
	_ string,
	_ ports.ScriptTranslator,
) (*scriptpkg.ModelScriptOutputV1, []string, error) {
	return nil, nil, scriptpkg.ErrPostprocessFailed
}
