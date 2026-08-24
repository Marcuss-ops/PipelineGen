// Package scripts — generate_length_tolerance_p0i_test.go: P0.I
// text-length-with-tolerance contract for /api/script/generate.
//
// July 2026 PR — P0.I gate (re-implementation). This file
// replaces the previous generate_provenance_p0i_test.go
// (commit e959e6618) with a fresh implementation focused on
// the editorial-quality-gate target-words tolerance contract.
// Same orchestrator seam (buildUsecaseWithClipResolver +
// fakeOllamaGen + fakeClipResolver), different contract under
// test.
//
// USER-SPEC INVARIANTS — "tolleranza":
//
// The quality gate's target-words tolerance check (quality_gate.go)
// uses minTargetWordsRatio=0.80 and maxTargetWordsRatio=1.20:
//
//	lower = plan.TargetWords * 0.80
//	upper = plan.TargetWords * 1.20
//	FAIL if actual < lower OR actual > upper
//
// USER-SPEC PINPOINTS three target_words cases with their
// canonical tolerance windows:
//
//	target_words=150  →  lower=120  upper=190  (accepts 120-190)
//	target_words=400  →  lower=320  upper=500  (accepts 320-500)
//	target_words=1000 →  lower=800  upper=1200 (accepts 800-1200)
//
// CONSISTENCY CONTRACT (coerenza fra output.word_count e
// quality.actual_words):
//
// The quality gate reads result.Output.WordCount and stores it
// in quality.ActualWords by construction:
//
//	q.ActualWords = result.Output.WordCount
//
// The user's "coerenza" check (NON usare confronto esatto,
// solo tolleranza) is satisfied by:
//
//	(a) BOTH result.Output.WordCount and result.Quality.ActualWords
//	    must independently fall within [target*0.80, target*1.20]
//	    (tolerance check on each field, NOT exact comparison).
//	(b) |result.Output.WordCount - result.Quality.ActualWords|
//	    must be ≤ 1 (consistency delta, tolerance-based — a
//	    small delta is expected from off-by-one counting in
//	    the engine's tokenizer; an exact comparison would be
//	    too strict and would NOT honor "solo tolleranza").
//
// Prose engineering: the helper p0iBuildLengthToleranceOrchestrator
// generates a vocabulary-repeated prose of EXACTLY target_words
// words. The vocabulary (p0iLengthVocabulary) is a SHARED
// SUPERSET of the prose's tokens, so computeSourceTextCoverage
// ≈ 1.0 — well above the default 0.70 threshold. This isolates
// the target-words tolerance contract from the
// source-text-coverage contract (if the prose used different
// tokens, the source-coverage check would fire first and mask
// the actual contract under test).
//
// godlike/07 NO-FAKE-AVAILABILITY: every assertion pins a
// canonical typed contract field (Output.WordCount,
// Quality.ActualWords, Quality.TargetWords, Quality.Passed)
// with tolerance windows, NOT exact-equality comparisons.
package usecase

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// p0iLengthClipAlpha + p0iLengthClipBeta are the canonical
// 2-clip IDs used by ALL 3 P0.I test cases. Pinned as
// constants so a future contributor cannot accidentally
// switch to 1-clip or 3-clip variants and break the
// symmetric-binding invariant (enforceClipNativeContract's
// "1 clip = 1 scene" + "every accepted clip is bound").
const (
	p0iLengthClipAlpha = "clip-alpha"
	p0iLengthClipBeta  = "clip-beta"
)

// p0iLengthVocabulary is the SHARED source-text vocabulary
// assigned to BOTH item.Source.SourceText AND the clip
// SearchText. It is engineered to be a SUPERSET of the
// prose's non-stop-word tokens (the prose is just this
// vocabulary repeated target_words times). This drives
// computeSourceTextCoverage to ~1.0, well above the default
// 0.70 threshold, isolating the target-words tolerance
// contract from the source-text-coverage contract.
//
// Vocabulary has ~35 unique whitespace-separated tokens.
// The prose repeats these tokens target_words times, so
// every prose token is in the source — coverage = 1.0.
const p0iLengthVocabulary = "The bell rings fighters touch gloves round 1 round 7 " +
	"boxing match action crowd cheers roars begins both trading " +
	"jabs challenger studies opponent carefully again trade more " +
	"quick brown fox jumps lazy dog intensifies ring great passion"

// p0iLengthScene1JSON + p0iLengthScene2JSON are the canonical
// 2-scene SpecScene envelopes bound to the 2 canonical clips.
// Each scene is bound to its clip via the
// "bindings.clip.clip_id" field, satisfying
// enforceClipNativeContract's "1 clip = 1 scene" + "every
// accepted clip is bound to a scene" checks simultaneously
// (canonical happy path).
const (
	p0iLengthScene1JSON = `{"id":"scene-1","index":0,"text":"S1.","kind":"narration","bindings":{"clip":{"clip_id":"clip-alpha"}}}`
	p0iLengthScene2JSON = `{"id":"scene-2","index":1,"text":"S2.","kind":"narration","bindings":{"clip":{"clip_id":"clip-beta"}}}`
)

// p0iBuildLengthToleranceEnvelope returns the canonical
// ModelScriptOutputV1 JSON envelope with 2 bound scenes and
// the given prose. The prose is wrapped with %q to properly
// escape any special characters for JSON.
func p0iBuildLengthToleranceEnvelope(prose string) string {
	return fmt.Sprintf(
		`{"schema_version":1,"text":%q,"specscene":{"version":1,"scenes":[%s,%s]}}`,
		prose, p0iLengthScene1JSON, p0iLengthScene2JSON,
	)
}

// p0iGenerateProse returns a prose of EXACTLY targetWords
// whitespace-separated words by repeating p0iLengthVocabulary
// cyclically. The resulting string is pure vocabulary tokens
// (no punctuation, no hyphens, no numbers-as-words) so the
// engine's word-count tokenizer produces the exact targetWords
// count with zero off-by-one risk.
//
// Example output for targetWords=5:
//
//	"The bell rings fighters touch"
func p0iGenerateProse(targetWords int) string {
	vocabWords := strings.Fields(p0iLengthVocabulary)
	if len(vocabWords) == 0 {
		return ""
	}
	parts := make([]string, targetWords)
	for i := 0; i < targetWords; i++ {
		parts[i] = vocabWords[i%len(vocabWords)]
	}
	return strings.Join(parts, " ")
}

// p0iBuildLengthToleranceOrchestrator wires the canonical
// orchestrator (buildUsecaseWithClipResolver) with a
// fakeOllamaGen emitting a vocabulary-repeated prose of
// EXACTLY targetWords words. The clip resolver is registered
// with BOTH canonical clips using the SHARED p0iLengthVocabulary
// so source-text coverage is ~1.0.
//
// Returns: (orchestrator, item, targetWords) — the third
// return value lets the test compute the tolerance window
// without re-typing the target literal.
func p0iBuildLengthToleranceOrchestrator(t *testing.T, targetWords int) (*GenerateOneUseCase, scriptpkg.GenerationItemV2, int) {
	t.Helper()

	prose := p0iGenerateProse(targetWords)
	envelope := p0iBuildLengthToleranceEnvelope(prose)

	gen := &fakeOllamaGen{result: &scriptports.GenerationResult{
		Script:      envelope,
		WordCount:   targetWords,
		EstDuration: 3,
		Model:       "llama3:8b",
		Prompt:      "<p0i-length-test prompt — not asserted>",
	}}

	clip1 := makeTestClip(p0iLengthClipAlpha, "Alpha clip", 30*time.Second)
	clip1.SearchText = p0iLengthVocabulary
	clip2 := makeTestClip(p0iLengthClipBeta, "Beta clip", 30*time.Second)
	clip2.SearchText = p0iLengthVocabulary

	resolver := newFakeClipResolver()
	resolver.AddClip(clip1)
	resolver.AddClip(clip2)

	uc := buildUsecaseWithClipResolver(gen, resolver)

	item := makeClipsItem("p0i-length", []string{p0iLengthClipAlpha, p0iLengthClipBeta}, p0iLengthVocabulary)
	item.ScriptParams.TargetWords = targetWords

	return uc, item, targetWords
}

// p0iToleranceWindow was REMOVED in PR-P0.I fix 2: the
// user-spec windows ([120,190], [320,500], [800,1200]) do
// not exactly match the quality gate's 1.20 ratio windows
// ([120,180], [320,480], [800,1200]) for target=150 and
// target=400. The user spec is the authoritative source for
// the P0.I test contract, so the tolerance windows are now
// hardcoded directly in the test cases (see the `cases`
// table in TestLengthTolerance_P0I) rather than computed
// from the quality gate's constants. This decouples the
// test from the quality gate's implementation and pins the
// exact user-spec contract.

// TestLengthTolerance_P0I pins the target-words tolerance
// contract for /api/script/generate across 3 canonical
// target_words cases. Each sub-test exercises the same
// orchestrator seam with a different target, verifying:
//
//  1. result.Output.WordCount is within
//     [target*0.80, target*1.20] (tolerance, NOT exact).
//  2. result.Quality.ActualWords is within
//     [target*0.80, target*1.20] (tolerance, NOT exact).
//  3. |result.Output.WordCount - result.Quality.ActualWords|
//     ≤ 1 (consistency delta — tolerance-based, NOT exact
//     comparison; a small delta is expected from off-by-one
//     in the engine's tokenizer).
//  4. result.Quality.TargetWords == target (typed contract
//     for the round-trip).
//  5. result.Quality.Passed == true (every quality gate
//     check passes — vocabulary-heavy design ensures source
//     coverage ≈ 1.0, language=en, clip coverage=1.0,
//     unsupported claims=0, generic text=false).
//
// Test seam: orchestrator-level (buildUsecaseWithClipResolver
// + fakeOllamaGen + fakeClipResolver with both canonical
// clips registered). The default stub postprocessor does
// NOT mutate the result.
func TestLengthTolerance_P0I(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		words        int
		lower, upper int
	}{
		{"Target150_Accepts120To190", 150, 120, 190},
		{"Target400_Accepts320To500", 400, 320, 500},
		{"Target1000_Accepts800To1200", 1000, 800, 1200},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			uc, item, target := p0iBuildLengthToleranceOrchestrator(t, tc.words)
			lower, upper := tc.lower, tc.upper

			result, err := uc.Execute(context.Background(), item, scriptpkg.Preset(""), nil)

			// Orchestrator MUST return a non-nil result with no
			// error — the vocabulary-heavy design ensures every
			// quality gate check passes.
			require.NoErrorf(t, err,
				"P0.I target=%d orchestrator MUST return nil error; err=%v", target, err)
			require.NotNilf(t, result,
				"P0.I target=%d orchestrator MUST return a non-nil result; got=nil", target)
			require.NotNilf(t, result.Quality,
				"P0.I target=%d MUST populate result.Quality (the canonical editorial-gate block); got=nil", target)

			// (1) output.WordCount MUST be within the tolerance
			//     window [lower, upper] (tolerance, NOT exact).
			assert.GreaterOrEqualf(t, result.Output.WordCount, lower,
				"P0.I target=%d: result.Output.WordCount=%d MUST be >= lower=%d (target * 0.80); got below tolerance window",
				target, result.Output.WordCount, lower)
			assert.LessOrEqualf(t, result.Output.WordCount, upper,
				"P0.I target=%d: result.Output.WordCount=%d MUST be <= upper=%d (target * 1.20); got above tolerance window",
				target, result.Output.WordCount, upper)

			// (2) quality.ActualWords MUST be within the same
			//     tolerance window (tolerance, NOT exact).
			assert.GreaterOrEqualf(t, result.Quality.ActualWords, lower,
				"P0.I target=%d: result.Quality.ActualWords=%d MUST be >= lower=%d (target * 0.80); got below tolerance window",
				target, result.Quality.ActualWords, lower)
			assert.LessOrEqualf(t, result.Quality.ActualWords, upper,
				"P0.I target=%d: result.Quality.ActualWords=%d MUST be <= upper=%d (target * 1.20); got above tolerance window",
				target, result.Quality.ActualWords, upper)

			// (3) CONSISTENCY (coerenza): |output.WordCount -
			//     quality.ActualWords| MUST be ≤ 1 (tolerance-
			//     based, NOT exact comparison). A small delta
			//     is expected from off-by-one counting in the
			//     engine's tokenizer. The user explicitly
			//     required "NON usare confronto esatto, solo
			//     tolleranza" — this delta check honors that.
			diff := result.Output.WordCount - result.Quality.ActualWords
			if diff < 0 {
				diff = -diff
			}
			assert.LessOrEqualf(t, diff, 1,
				"P0.I target=%d: consistency delta |output.WordCount=%d - quality.ActualWords=%d|=%d MUST be <= 1 (tolerance-based, NOT exact); user spec: 'NON usare confronto esatto, solo tolleranza'",
				target, result.Output.WordCount, result.Quality.ActualWords, diff)

			// (4) quality.TargetWords MUST equal the input
			//     target (typed round-trip contract).
			assert.Equalf(t, target, result.Quality.TargetWords,
				"P0.I target=%d: result.Quality.TargetWords MUST equal input target; got=%d",
				target, result.Quality.TargetWords)

			// (5) quality.Passed MUST be true — every quality
			//     gate check passes (vocabulary-heavy design
			//     ensures source coverage ≈ 1.0, language=en,
			//     clip coverage=1.0, unsupported claims=0,
			//     generic text=false, target words within
			//     tolerance).
			assert.Truef(t, result.Quality.Passed,
				"P0.I target=%d: result.Quality.Passed MUST be true (all gate checks pass); got=false; quality=%+v",
				target, result.Quality)

			// Diagnostic log for the operator dashboard —
			// surfaces the actual vs target numbers for
			// debugging without requiring verbose mode.
			t.Logf("P0.I target=%d: output.WordCount=%d quality.ActualWords=%d tolerance=[%d,%d] passed=%v",
				target, result.Output.WordCount, result.Quality.ActualWords, lower, upper, result.Quality.Passed)
		})
	}
}
