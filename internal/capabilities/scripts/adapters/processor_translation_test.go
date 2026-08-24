// Package adapters — processor_translation_test.go: Phase 1 B1 TDD regression guard.
//
// TestTranslationProcessor_UsesPlanTranslateTo pins the canonical contract
// that TranslationProcessor.Process MUST respect plan.TranslateTo as the
// authoritative target-language signal when the operator requests a
// non-default translation direction.
//
// Pre-fix bug (B1):
//   - Production code at internal/application/scripts/adapters/processor_translation.go
//     resolves `targetLang` from `plan.Languages[0]` (then falls back to
//     `plan.Language`), never consulting `plan.TranslateTo`.
//   - With plan.Languages=["en"] but plan.TranslateTo="it", the translator
//     is invoked with targetLanguage="en" — the WRONG language.
//   - Symptom: "translate this English script into Italian" is silently
//     translated as English-or-untranslated.
//
// Post-fix expectation:
//   - plan.TranslateTo (when non-empty) MUST take precedence over
//     plan.Languages[0] (and over the legacy plan.Language fallback).
//   - The recording translator MUST observe targetLanguage="it".
//
// This test MUST FAIL on current production code (it confirms the bug is
// present in origin/main).
package adapters

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// recordingTranslator is a deterministic ports.ScriptTranslator
// implementation that records the LAST targetLanguage argument
// passed to Translate so the test can assert which source-of-truth
// the processor resolved it from (plan.TranslateTo vs plan.Languages[0]
// vs plan.Language). It returns the source text unchanged unless the
// caller provides a custom translate closure.
type recordingTranslator struct {
	lastLang string
	lastText string
	calls    int
}

func (r *recordingTranslator) Translate(_ context.Context, text, targetLanguage string) (string, error) {
	r.calls++
	r.lastText = text
	r.lastLang = targetLanguage
	return text, nil // identity translator — B1 surface doesn't care about output, only targetLang
}

// Compile-time conformance pin (godlike/06 SSOT one canonical owner per fact):
var _ ports.ScriptTranslator = (*recordingTranslator)(nil)

// successUseCase is a deterministic ports.TranslationUseCase
// implementation that records the LAST targetLang argument passed
// to TranslateScriptSpec and returns the input envelope as the
// translated result (identity translation). B1 only cares about
// which targetLang the processor resolved from the plan — the
// translated surface itself is NOT under test (that's B2/B3's
// concern). Using a success-returning use case (instead of the
// canonical noop fallback) is what lets B1 reach the targetLang
// assertion: the noop fallback returns errNoopTranslationUseCase
// which causes the test to fatal on err != nil BEFORE the
// translation-flow logic runs — masking the real bug behind a
// "wire error" (godlike/07 NO-FAKE-AVAILABILITY pin).
type successUseCase struct {
	lastTargetLang string
	calls          int
}

func (s *successUseCase) TranslateScriptSpec(
	_ context.Context,
	in *scriptpkg.ModelScriptOutputV1,
	_ *scriptpkg.ClipEvidence,
	targetLang string,
	_ ports.ScriptTranslator,
) (*scriptpkg.ModelScriptOutputV1, []string, error) {
	s.calls++
	s.lastTargetLang = targetLang
	// Identity translation: B1 surface doesn't care about output,
	// only the targetLang the production code resolved from the
	// plan (the actual mutation is covered by B2's
	// TestMergePostProcessResult_PropagatesTranslatedToCurrentInput
	// + B3's TestBuildGenerationResult_PrefersTranslatedOutput).
	return in, nil, nil
}

// Compile-time conformance pin (godlike/06 SSOT one canonical owner per fact):
var _ ports.TranslationUseCase = (*successUseCase)(nil)

// TestTranslationProcessor_UsesPlanTranslateTo is the canonical Phase 1 /
// Bug B1 regression guard.
//
// Plan setup (canonical scenario):
//   - plan.Languages   = ["en"] // the canonical SCOPE list; never used as translate-to
//   - plan.Language    = "en"   // dispatch language; never used as translate-to
//   - plan.TranslateTo = "it"   // operator's "translate this English script into Italian" signal
//
// Current behaviour (BUG): translator sees targetLanguage="en" because the
// processor picks `plan.Languages[0]`. Post-fix canonical: translator sees
// "it". On current production code this assertion FAILS.
func TestTranslationProcessor_UsesPlanTranslateTo(t *testing.T) {
	// ── Arrange ────────────────────────────────────────────────────────────
	ctx := context.Background()
	const wantTarget = "it" // plan.TranslateTo = "it" (canonical operator signal)

	rec := &recordingTranslator{}
	uc := &successUseCase{}
	proc := NewTranslationProcessor(
		rec, // ScriptTranslator (defensive: no-op stub since successUseCase ignores the translator arg)
		ports.NewNoopTranslationMetricsRecorder(), // TranslationMetricsRecorder (no-op)
		uc, // TranslationUseCase (success-returning — records targetLang)
		ports.NewNoopTranslationReasonClassifier(), // TranslationReasonClassifier (no-op)
		zap.NewNop(), // log
	)

	// Minimal ResolvedGenerationPlan with TranslateTo set as the
	// operator signal. Languages[0]="en" is the dummy "wrong" answer
	// that the bug prefers because of Languages-first reading.
	plan := &scriptpkg.ResolvedGenerationPlan{
		Languages:   []string{"en"},
		Language:    "en",
		TranslateTo: wantTarget,
	}

	// Minimal ProcessInput (value type — canonical signature).
	// Only Text is read by TranslationProcessor in the happy path.
	srcText := "twitter fights break out after a heated exchange"
	nonEmptyScene := scriptpkg.SpecScene{
		Index: 0,
		Kind:  scriptpkg.SceneClip,
		Text:  srcText,
	}
	input := ProcessInput{
		Text: srcText,
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes:  []scriptpkg.SpecScene{nonEmptyScene},
		},
	}

	// Pre-flight sanity-check the stubs' lifecycles so a future
	// refactor that silently injects a wrapper (or one that swallows
	// the target-lang arg via a typed-port boundary) surfaces here as
	// a build-time signal (godlike/07 NO-FAKE-AVAILABILITY pin).
	if rec.calls != 0 {
		t.Fatalf("recordingTranslator should start with 0 calls, got %d", rec.calls)
	}
	if uc.calls != 0 {
		t.Fatalf("successUseCase should start with 0 calls, got %d", uc.calls)
	}

	// ── Act ────────────────────────────────────────────────────────────────
	// Process signature (post cycle-breaking refactor):
	//   func (p *TranslationProcessor) Process(ctx, plan *ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error)
	_, err := proc.Process(ctx, plan, input)

	// ── Assert ─────────────────────────────────────────────────────────────
	// (A) Process must have completed WITHOUT error so a future fix that
	// errors out on a phantom TranslateTo is NOT silently masked by an
	// earlier error path.
	if err != nil {
		t.Fatalf("TranslationProcessor.Process returned unexpected error: %v", err)
	}

	// (B) The translator MUST have observed targetLanguage="it" because
	// plan.TranslateTo="it" is the canonical operator signal.
	// Pre-fix bug: production reads plan.Languages[0]="en" → lastLang="en"
	// → this assertion FAILS (the Phase 1 B1 regression guard).
	if uc.calls < 1 {
		t.Fatalf("successUseCase should observe at least 1 TranslateScriptSpec call "+
			"(happy-path processor must reach the use case), got %d",
			uc.calls)
	}
	// B1 regression guard: the production code resolves targetLang from
	// plan.Languages[0] (line "if len(plan.Languages) > 0 { targetLang =
	// strings.TrimSpace(plan.Languages[0]) }" in processor_translation.go),
	// never consulting plan.TranslateTo. With the canonical scenario
	// (plan.Languages=["en"], plan.TranslateTo="it"), the use case
	// observes targetLang="en" — this assertion FAILS. The post-fix
	// canonical contract: plan.TranslateTo takes precedence over
	// plan.Languages[0] when both are set.
	if lastLang := uc.lastTargetLang; lastLang != wantTarget {
		t.Errorf("successUseCase.lastTargetLang = %q, want %q "+
			"(plan.TranslateTo MUST take precedence over plan.Languages[0]="+
			" – this is the Phase 1 B1 regression guard; production code reads "+
			"plan.Languages[0] first which is the bug)",
			lastLang, wantTarget)
	}
}
