// Package usecase — engine_generate_test.go: Phase 1 B3 TDD regression guard.
//
// TestBuildGenerationResult_PrefersTranslatedOutput pins the canonical
// contract that buildGenerationResult MUST prefer postResult.TranslatedText
// over engineResult.Output.Text when the downstream TranslationProcessor
// has produced a translated surface.
//
// Pre-fix bug (B3):
//   - Production code at internal/application/scripts/usecase/persistence.go
//     sets `Output: ScriptOutput{ Text: engineResult.Output.Text, ... }` -
//     it copies the EngineResult's text unconditionally, IGNORING
//     `postResult.TranslatedText`.
//   - Symptom: a successful translation (PipelineResult.TranslatedText="italian")
//     is silently dropped at the persistence step; the persisted
//     GenerationResult.Output.Text is the ORIGINAL English text the model
//     emitted pre-translation.
//
// Post-fix expectation:
//   - When postResult.TranslatedText is non-empty, result.Output.Text MUST
//     equal postResult.TranslatedText (NOT engineResult.Output.Text).
//   - This is the canonical godlike/07 "prefer downstream-processed
//     surface" pattern: an upstream layer may have left a stale Text on
//     engineResult.Output.Text, but a downstream processor (Translation)
//     refined the surface — the merge step MUST honour the refined surface.
//
// This test MUST FAIL on current production code (it confirms the bug).
package usecase

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestBuildGenerationResult_PrefersTranslatedOutput is Phase 1 / Bug B3 regression guard.
//
// Canonical scenario:
//   - engineResult.Output.Text = "I will defeat you."       (English, pre-translation)
//   - postResult (PipelineResult).TranslatedText = "Sconfiggerò." (Italian, post-translation)
//   - buildGenerationResult(...) MUST produce result.Output.Text = "Sconfiggerò."
//
// Variant coverage (single test enumerates 3 cases inline for hermetic clarity):
//  1. Happy path: postResult.TranslatedText non-empty → Output.Text MUST = it.
//  2. Fallback: postResult.TranslatedText is empty → Output.Text MUST = en
//     (pre-fix already does this; post-fix MUST preserve this behaviour).
//  3. nil-postResult (defensive nil-tolerance guarantee): Output.Text MUST = en
//     (the engineResult is the only source when postResult is unwired).
func TestBuildGenerationResult_PrefersTranslatedOutput(t *testing.T) {
	// ── Arrange (canonical scenario, every case reuses these constants) ──
	const english = "I will defeat you."
	const italian = "Sconfiggerò."

	// Minimal EngineResult (the pre-translation engine output).
	engineOutput := scriptpkg.ModelScriptOutputV1{
		Text: english,
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes:  nil, // B3 is about Text only; SpecScene topology is B-other-tests' scope.
		},
	}
	engineResult := &EngineResult{
		Output:      engineOutput,
		WordCount:   5,
		Model:       "test-model",
		CacheStatus: "miss",
	}

	// Minimal GenerationItemV2 (the function only reads field-level data
	// for ScriptID lookup; we leave it zero-valued for hermetic clarity).
	item := scriptpkg.GenerationItemV2{}

	// Minimal ResolvedGenerationPlan.
	plan := scriptpkg.ResolvedGenerationPlan{}

	// ── Case 1: Happy path (postResult.TranslatedText non-empty) ────────────
	t.Run("TranslatedNonEmpty_PrefersPostResultTranslatedText", func(t *testing.T) {
		post := &adapters.PipelineResult{
			TranslatedText: italian, // canonical post-translation surface
		}

		// ── Act ──
		result := buildGenerationResult(item, plan, engineResult, post)

		// ── Assert ──
		if result == nil {
			t.Fatalf("buildGenerationResult returned nil result post-fixable")
		}
		if got := result.Output.Text; got != italian {
			t.Errorf("result.Output.Text = %q, want %q "+
				"(buildGenerationResult must prefer postResult.TranslatedText over "+
				"engineResult.Output.Text when the downstream translation produced "+
				"a non-empty surface — current production code copies engineResult.Output.Text "+
				"unconditionally, which is the bug)",
				got, italian)
		}
	})

	// ── Case 2: Fallback (postResult nil — defensive nil-tolerance) ────────
	t.Run("PostResultNil_FallsBackToEngineResultOutputText", func(t *testing.T) {
		// ── Act ──
		result := buildGenerationResult(item, plan, engineResult, nil)

		// ── Assert ──
		if result == nil {
			t.Fatalf("buildGenerationResult returned nil result for nil postResult")
		}
		if got := result.Output.Text; got != english {
			t.Errorf("result.Output.Text = %q, want %q (fallback to engineResult.Output.Text "+
				"when postResult is nil — this is the canonical pre-fix behaviour and "+
				"post-fix MUST preserve it)",
				got, english)
		}
	})

	// ── Case 3: Fallback (postResult.TranslatedText empty) ────────────────
	t.Run("PostResultTranslatedTextEmpty_FallsBackToEngineResultOutputText", func(t *testing.T) {
		post := &adapters.PipelineResult{
			TranslatedText: "", // empty - signal = "no translation produced"
		}

		// ── Act ──
		result := buildGenerationResult(item, plan, engineResult, post)

		// ── Assert ──
		if result == nil {
			t.Fatalf("buildGenerationResult returned nil result for empty postResult.TranslatedText")
		}
		if got := result.Output.Text; got != english {
			t.Errorf("result.Output.Text = %q, want %q (fallback to engineResult.Output.Text "+
				"when postResult.TranslatedText is empty - canonical 'no translation succeeded' signal)",
				got, english)
		}
	})
}
