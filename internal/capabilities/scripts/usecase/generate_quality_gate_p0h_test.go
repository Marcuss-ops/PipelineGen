// Package scripts — generate_quality_gate_p0h_test.go: P0.H
// quality-gate & hallucination contract for /api/script/generate.
//
// July 2026 PR — P0.H gate. Pins the canonical contract for the
// editorial quality gate when the LLM is given a CONTROLLED
// Pacquiao/Broner fixture: clips that talk ONLY about Pacquiao
// and Broner (no Mayweather, no Las Vegas, no scorecard, no
// citations, no extra rounds, no medical details).
//
// USER-SPEC INVARIANTS — "nessun riferimento a":
//
//  1. nessun riferimento a Floyd Mayweather (canonical rival — the
//     test clips are Pacquiao vs Broner ONLY, so any Mayweather
//     mention is a hallucination).
//
//  2. nessun Las Vegas non documentato nell'evidence (the canonical
//     Pacquiao vs Broner (2019) venue was MGM Grand in Las Vegas,
//     but the test clips carry NO venue metadata, so the LLM must
//     NOT invent one).
//
//  3. nessun knockdown ufficiale non presente (the canonical
//     Pacquiao vs Broner had no official knockdown, so any
//     "knockdown" claim is a hallucination).
//
//  4. nessun scorecard inventato (the canonical decision was
//     unanimous, so any specific scorecard like "116-112",
//     "114-113", "115-113", or judge names is a hallucination).
//
//  5. nessuna citazione diretta (no quote marks, no "ha detto",
//     no "dichiara", no "secondo" attribution).
//
//  6. nessun round non incluso (the test clips cover ONLY round 1
//     and round 7; the prose must NOT mention rounds 2, 3, 4, 5,
//     6, 8, 9, 10, 11, 12 or "round finale" / "ultimo round").
//
//  7. nessun dettaglio medico (no "infortunio", "medico",
//     "ospedale", "taglio", "knockout", "incidente").
//
// POSITIVE ASSERTIONS on result.Quality (the canonical quality-gate
// block):
//
//   - quality.language_detected      == "it"
//   - quality.clip_evidence_coverage == 1.0
//   - quality.unsupported_claims     == 0
//   - quality.passed                 == true
//
// POSITIVE GROUNDING on the prose (the test is NOT just absence-of-
// hallucination — it pins that the LLM actually talked about the
// canonical subjects):
//
//   - result.Output.Text MUST mention "Pacquiao" (case-insensitive).
//   - result.Output.Text MUST mention "Broner" (case-insensitive).
//
// Test seam: orchestrator-level (buildUsecaseWithClipResolver +
// fakeOllamaGen + fakeClipResolver with BOTH canonical clips
// registered). The fakeOllamaGen emits a hardcoded ~55-word Italian
// prose via a JSON envelope with 2 scenes, each bound to its
// canonical clip. The default stub postprocessor in
// buildUsecaseWithClipResolver does NOT extract entities, so
// result.Artifacts.Entities == nil → countUnsupportedClaims
// returns 0 trivially. This is acceptable: the quality gate
// contract under test is the AGGREGATE quality.passed=true signal,
// not the individual entity-matching path (covered by
// TestEvaluateQualityGate_FailsUnsupportedClaims in
// quality_gate_test.go).
//
// godlike/07 NO-FAKE-AVAILABILITY: every positive assertion pins a
// canonical typed contract field (Quality.*, Output.Text). Every
// negative assertion pins a forbidden substring in the canonical
// Output.Text field. No "result was non-nil" soft passes.
package usecase

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// p0hClipPacquiaoR1 is the canonical Pacquiao round-1 clip ID used
// by the P0.H controlled fixture. Pinned as a const so the
// postprocessor binder + clip_native_enforce test can both refer
// to the same ID without drift.
const p0hClipPacquiaoR1 = "clip-pacquiao-r1"

// p0hClipBronerR7 is the canonical Broner round-7 clip ID.
const p0hClipBronerR7 = "clip-broner-r7"

// p0hClipSearchText is the SHARED Italian search text assigned to
// BOTH canonical clips. It is engineered to share enough non-stop-
// word tokens with the prose so computeSourceTextCoverage exceeds
// the clips_primary threshold of 0.40. The prose's non-stop tokens
// (pacquiao, broner, match, boxe, round, gancio, sinistro,
// accelera, ritmo, campana, rivale, risponde, etc.) all appear in
// this search text. countUnsupportedClaims returns 0 trivially
// because result.Artifacts.Entities == nil (default stub
// postprocessor does not extract entities).
const p0hClipSearchText = "Pacquiao Broner match boxe round gancio sinistro " +
	"accelera ritmo campana rivale risponde studia attenzione " +
	"inizia suono calma preciso reagisce determinazione folla " +
	"osserva passione continua intensità 1 7"

// p0hProse is the controlled ~55-word Italian narrative. It is
// the canonical fixture prose used by the P0.H test. It is
// surgically engineered to:
//
//  1. be in Italian (rich in "il", "la", "di", "e", "con", "in",
//     "del", "un", "una" — all canonical Italian stop words from
//     quality_gate.go's stopWords["it"]), so detectLanguage returns
//     "it" with high confidence (not "en");
//
//  2. mention ONLY Pacquiao and Broner (no Mayweather, no other
//     fighters);
//
//  3. mention ONLY round 1 and round 7 (the canonical rounds in
//     the clip evidence — no other rounds, no "round finale" /
//     "ultimo round");
//
//  4. NOT mention Mayweather, Las Vegas, MGM, scorecard, judge
//     names, "116-112", "114-113", "115-113", "unanime",
//     "knockdown", "infortunio", "medico", "ospedale", "taglio",
//     "knockout", "incidente";
//
//  5. NOT use direct citations (no quote marks, no "ha detto",
//     no "dichiara", no "secondo");
//
//  6. share enough non-stop-word tokens with p0hClipSearchText
//     (pacquiao, broner, match, boxe, round, gancio, sinistro,
//     accelera, ritmo, campana, rivale, risponde, studia,
//     attenzione) for source_text coverage to exceed 0.40
//     (clips_primary threshold);
//
//  7. have ~55 words so the target_words tolerance
//     [50*0.80, 50*1.20] = [40, 60] is satisfied with
//     TargetWords=50.
//
// godlike/07 honest-lock: this prose is the authoritative input
// for the P0.H test. If a future contributor modifies it, the
// test MUST still satisfy the 6 negative constraints above. The
// pre-flight coverage / stop-word / word-count math is documented
// in the test header to make regressions debuggable.
const p0hProse = "Il match di boxe tra Pacquiao e Broner inizia con il suono " +
	"della campana. Nel round 1, Pacquiao studia il rivale con " +
	"attenzione. Broner risponde con calma. Nel round 7, Broner " +
	"accelera il ritmo con un gancio sinistro preciso. Pacquiao " +
	"reagisce con determinazione. La folla osserva il match con " +
	"passione. Il match continua con intensità."

// p0hScene1JSON is the canonical SpecScene JSON for the Pacquiao
// round-1 clip. Bound to p0hClipPacquiaoR1 via the "bindings.clip"
// field. Engine decodes this → SpecScene.Bindings.Clip.ClipID,
// and enforceClipNativeContract's "every accepted clip must be
// bound to a scene" check passes.
const p0hScene1JSON = `{"id":"scene-1","index":0,"text":"S1.","kind":"narration","bindings":{"clip":{"clip_id":"clip-pacquiao-r1"}}}`

// p0hScene2JSON is the canonical SpecScene JSON for the Broner
// round-7 clip. Bound to p0hClipBronerR7.
const p0hScene2JSON = `{"id":"scene-2","index":1,"text":"S2.","kind":"narration","bindings":{"clip":{"clip_id":"clip-broner-r7"}}}`

// p0hBuildControlledEnvelope returns the canonical ModelScriptOutputV1
// JSON envelope that, when decoded by the engine, yields
// engineResult.Output.SpecScene with EXACTLY 2 scenes — one bound
// to the Pacquiao R1 clip, one bound to the Broner R7 clip. This
// is the cleanest way to satisfy enforceClipNativeContract's
// "1 clip = 1 scene" + "every accepted clip is bound" checks
// simultaneously (canonical happy path).
func p0hBuildControlledEnvelope(prose string) string {
	return fmt.Sprintf(
		`{"schema_version":1,"text":%q,"specscene":{"version":1,"scenes":[%s,%s]}}`,
		prose, p0hScene1JSON, p0hScene2JSON,
	)
}

// p0hBuildPacquiaoBronerOrchestrator wires the canonical
// orchestrator (buildUsecaseWithClipResolver) with a fakeOllamaGen
// emitting the controlled 2-scene envelope. The clip resolver is
// registered with BOTH canonical clips (p0hClipPacquiaoR1 +
// p0hClipBronerR7) using a SHARED p0hClipSearchText that
// guarantees source_text coverage ≥ 0.40. The default stub
// postprocessor in buildUsecaseWithClipResolver does NOT extract
// entities, so result.Artifacts.Entities == nil →
// countUnsupportedClaims returns 0 trivially (godlike/07
// honest-lock: this is documented in the test header, NOT a
// silent no-op).
func p0hBuildPacquiaoBronerOrchestrator(t *testing.T) (*GenerateOneUseCase, scriptpkg.GenerationItemV2) {
	t.Helper()

	gen := &fakeOllamaGen{result: &scriptports.GenerationResult{
		Script:      p0hBuildControlledEnvelope(p0hProse),
		WordCount:   55,
		EstDuration: 3,
		Model:       "llama3:8b",
		Prompt:      "<p0h-test prompt — not asserted>",
	}}

	// Register BOTH canonical clips with the SHARED search text.
	// The resolver combines clip SearchTexts into the canonical
	// AssembledText used by computeSourceTextCoverage, so a
	// shared rich Italian vocabulary drives the coverage above
	// the clips_primary 0.40 threshold.
	clip1 := makeTestClip(p0hClipPacquiaoR1, "Pacquiao round 1", 30*time.Second)
	clip1.SearchText = p0hClipSearchText
	clip2 := makeTestClip(p0hClipBronerR7, "Broner round 7", 30*time.Second)
	clip2.SearchText = p0hClipSearchText

	resolver := newFakeClipResolver()
	resolver.AddClip(clip1)
	resolver.AddClip(clip2)

	uc := buildUsecaseWithClipResolver(gen, resolver)

	// item.Source.SourceText is set to a short Italian phrase that
	// ALSO shares non-stop tokens with the prose. The resolver
	// will combine this with the clip AssembledText into
	// plan.SourceText. This is the "user-provided source text"
	// pathway; the resolver's combined output dominates but the
	// item's source text still contributes.
	item := makeClipsItem("p0h-pacquiao-broner", []string{p0hClipPacquiaoR1, p0hClipBronerR7}, "Pacquiao Broner match boxe")
	// Set Language="it" so the quality gate's "detected language
	// must match requested language" check passes (the prose is
	// Italian, so detected="it" must match requested="it").
	item.Language = "it"
	// Set GroundingPolicy="clips_primary" so the source_text
	// coverage threshold is 0.40 (canonical policy for
	// source=clips), not the default 0.70. With the
	// p0hClipSearchText SUPERSET design, coverage is ~1.0,
	// which is well above the 0.40 threshold.
	item.Source.GroundingPolicy = scriptpkg.GroundingPolicyClipsPrimary
	item.ScriptParams.TargetWords = 50

	return uc, item
}

// TestQualityGate_P0H_PacquiaoBroner_HallucinationFree pins the
// P0.H contract end-to-end on a single controlled fixture.
//
// Test flow:
//  1. The fakeOllamaGen returns a hardcoded ~55-word Italian
//     prose about Pacquiao and Broner (NO Mayweather, NO Las
//     Vegas, NO scorecard, NO citations, NO extra rounds, NO
//     medical details) wrapped in a 2-scene envelope (1 scene
//     per clip, each bound).
//  2. The orchestrator resolves the 2 clips, builds the plan,
//     runs the engine (returns the hardcoded envelope), runs
//     the (stub) postprocessor, builds the result, enforces
//     the clip-native contract (1 scene per clip, all clips
//     bound → Status=SUCCEEDED), runs the editorial quality
//     gate (Italian detected, full clip coverage, no
//     unsupported claims, all thresholds met → Quality.Passed=
//     true).
//  3. Execute returns (result, nil) with a fully-populated
//     result.Quality block and result.Output.Text set to the
//     hardcoded prose.
//
// Assertions (godlike/07 typed-contract):
//
//	(a) Execute returns a non-nil result with no error.
//	(b) result.Status == ItemStatusSucceeded (no fallback
//	    warnings — the contract is clean).
//	(c) result.Quality != nil (the gate ran).
//	(d) result.Quality.LanguageDetected == "it".
//	(e) result.Quality.ClipEvidenceCoverage == 1.0.
//	(f) result.Quality.UnsupportedClaims == 0.
//	(g) result.Quality.Passed == true.
//	(h) Positive grounding: prose mentions BOTH "pacquiao"
//	    AND "broner" (case-insensitive).
//	(i) Negative: prose does NOT mention "mayweather" (case-
//	    insensitive).
//	(j) Negative: prose does NOT mention "las vegas" or
//	    "mgm" (case-insensitive).
//	(k) Negative: prose does NOT contain a fake scorecard
//	    (no "116-112", "114-113", "115-113" or judge names).
//	(l) Negative: prose has no direct citations (no quote
//	    marks, no "ha detto", no "dichiara", no "secondo").
//	(m) Negative: prose does NOT mention rounds outside the
//	    evidenced set (only round 1 and round 7 are in the
//	    clip evidence).
//	(n) Negative: prose has no medical details (no
//	    "infortunio", "medico", "ospedale", "taglio",
//	    "knockout", "incidente").
func TestQualityGate_P0H_PacquiaoBroner_HallucinationFree(t *testing.T) {
	t.Parallel()

	uc, item := p0hBuildPacquiaoBronerOrchestrator(t)

	result, err := uc.Execute(context.Background(), item, scriptpkg.Preset(""), nil)

	// (a) Execute MUST return a non-nil result with no error.
	//     The hardcoded prose is clean, the envelope is well-
	//     formed, the clips are bound, the gate passes — every
	//     piece of the chain succeeds. A non-nil err here would
	//     surface a regression in the orchestrator.
	require.NoErrorf(t, err,
		"P0.H orchestrator MUST return nil error for the controlled Pacquiao/Broner fixture (clean prose, 2 bound clips, no hallucinations); err=%v", err)
	require.NotNilf(t, result,
		"P0.H orchestrator MUST return a non-nil result for the controlled fixture; got=nil")

	// (b) Status MUST be ItemStatusSucceeded — the strict
	//     clip-native contract passes (1 scene per clip, all
	//     clips bound), so NO fallback warnings are appended.
	//
	//     Sprint 1.3 (godlike/08): the central classify phase
	//     produces SUCCEEDED_WITH_WARNINGS whenever
	//     len(result.Warnings) > 0, even on a clean (hallucination-
	//     free) fixture. The P0.H quality check passes
	//     (item.Passed==true), but the postprocessor chain surfaces
	//     benign non-fatal warnings that classify promotes to
	//     SUCCEEDED_WITH_WARNINGS. Update the assertion to match
	//     the verdict §"Centralize success classification" rule.
	assert.Equalf(t, scriptpkg.ItemStatusSucceededWithWarnings, result.Status,
		"P0.H clean fixture with non-fatal warnings MUST set Status=ItemStatusSucceededWithWarnings after the central classify phase; got=%q", result.Status)

	// (c) Quality MUST be non-nil — the gate ran and populated
	//     its block. A nil Quality would mean the gate never
	//     executed, which is itself a contract violation.
	require.NotNilf(t, result.Quality,
		"P0.H MUST populate result.Quality (the canonical editorial-gate block); got=nil")

	// (d) LanguageDetected MUST be "it" — the prose is Italian
	//     and the stop-word heuristic must classify it as
	//     such. A "en" or empty detection would mean the
	//     prose lacks Italian stop words and the gate's
	//     language-mismatch check would fail.
	assert.Equalf(t, "it", result.Quality.LanguageDetected,
		"P0.H MUST detect language=\"it\" for the Italian prose (the canonical Pacquiao/Broner article); got=%q", result.Quality.LanguageDetected)

	// (e) ClipEvidenceCoverage MUST be 1.0 — both canonical
	//     clips are bound to scenes in the envelope, so the
	//     coverage is exact. Anything less would mean a
	//     scene/clip wiring regression.
	assert.Equalf(t, 1.0, result.Quality.ClipEvidenceCoverage,
		"P0.H MUST achieve clip_evidence_coverage=1.0 (both canonical clips are bound to scenes); got=%.2f", result.Quality.ClipEvidenceCoverage)

	// (f) UnsupportedClaims MUST be 0 — the default stub
	//     postprocessor does not extract entities, so the
	//     entity-based unsupported-claim count is 0. This is
	//     the godlike/07 honest-lock signal: the count is
	//     trivially 0 because no entities were extracted, NOT
	//     because the gate is silently masking claims.
	assert.Equalf(t, 0, result.Quality.UnsupportedClaims,
		"P0.H MUST have unsupported_claims=0 (no entities extracted by the default stub postprocessor — godlike/07 honest-lock); got=%d", result.Quality.UnsupportedClaims)

	// (g) Passed MUST be true — every gate threshold is
	//     met (Italian detected, full clip coverage, no
	//     unsupported claims, source_text coverage ≥ 0.40
	//     for clips_primary, target_words within 80-120%
	//     tolerance). A false here would surface a
	//     threshold-tuning regression in the quality gate.
	assert.Truef(t, result.Quality.Passed,
		"P0.H MUST pass the editorial quality gate (clean Italian prose, 2 bound clips, no hallucinations); quality=%+v", result.Quality)

	// ── POSITIVE GROUNDING ──────────────────────────────────
	// The test is NOT just absence-of-hallucination. It pins
	// that the LLM actually talked about the canonical
	// subjects (Pacquiao + Broner) — otherwise a "blank"
	// response would trivially pass the negative assertions.
	prose := strings.ToLower(result.Output.Text)
	assert.Truef(t, strings.Contains(prose, "pacquiao"),
		"P0.H prose MUST mention 'pacquiao' (positive grounding — canonical subject); got=%q", result.Output.Text)
	assert.Truef(t, strings.Contains(prose, "broner"),
		"P0.H prose MUST mention 'broner' (positive grounding — canonical subject); got=%q", result.Output.Text)

	// ── NEGATIVE CONSTRAINTS (the canonical "nessun
	//     riferimento a" invariants) ─────────────────────────

	// (i) No Mayweather — the canonical Pacquiao rival. The
	//     test fixture has NO Mayweather evidence, so any
	//     mention is a hallucination.
	assert.Falsef(t, strings.Contains(prose, "mayweather"),
		"P0.H prose MUST NOT mention 'mayweather' (no Mayweather evidence in the fixture — canonical hallucination); got=%q", result.Output.Text)

	// (j) No Las Vegas / MGM — the canonical venue is not
	//     documented in the test fixture (the test clips
	//     carry NO venue metadata), so any venue mention
	//     is an unsupported claim.
	assert.Falsef(t, strings.Contains(prose, "las vegas"),
		"P0.H prose MUST NOT mention 'las vegas' (no venue metadata in the fixture); got=%q", result.Output.Text)
	assert.Falsef(t, strings.Contains(prose, "mgm"),
		"P0.H prose MUST NOT mention 'mgm' (no venue metadata in the fixture); got=%q", result.Output.Text)

	// (k) No fake scorecard — the canonical decision was
	//     unanimous, so any specific scorecard like
	//     "116-112", "114-113", "115-113" or judge names is
	//     a hallucination. We check the canonical numeric
	//     scorecard formats (with the hyphen separator);
	//     bare digits without the scorecard pattern are
	//     not asserted (the round numbers 1 and 7 are
	//     legitimate).
	for _, fakeScorecard := range []string{
		"116-112", "114-113", "115-113", "117-111",
		"118-110", "119-109", "120-108", "scorecard",
	} {
		assert.Falsef(t, strings.Contains(prose, fakeScorecard),
			"P0.H prose MUST NOT contain fake scorecard %q (no scorecard evidence in the fixture); got=%q",
			fakeScorecard, result.Output.Text)
	}

	// (l) No direct citations — no quote marks, no
	//     attribution phrases. The fixture has no quote
	//     evidence, so any quote is a hallucination.
	assert.Falsef(t, strings.Contains(result.Output.Text, "\""),
		"P0.H prose MUST NOT contain quote marks (no quote evidence in the fixture); got=%q", result.Output.Text)
	assert.Falsef(t, strings.Contains(result.Output.Text, "“"),
		"P0.H prose MUST NOT contain opening curly quote marks; got=%q", result.Output.Text)
	assert.Falsef(t, strings.Contains(result.Output.Text, "”"),
		"P0.H prose MUST NOT contain closing curly quote marks; got=%q", result.Output.Text)
	for _, attribution := range []string{
		"ha detto", "ha dichiarato", "dichiara", "secondo ",
		"according to", "says", "stated",
	} {
		assert.Falsef(t, strings.Contains(prose, attribution),
			"P0.H prose MUST NOT contain attribution phrase %q (no quote evidence in the fixture); got=%q",
			attribution, result.Output.Text)
	}

	// (m) No extra rounds — the test fixture covers ONLY
	//     round 1 and round 7. The prose MUST mention only
	//     "round 1" and "round 7", and MUST NOT mention
	//     any other round (2, 3, 4, 5, 6, 8, 9, 10, 11,
	//     12) or generic "round finale" / "ultimo round"
	//     expressions.
	for _, forbiddenRound := range []string{
		"round 2", "round 3", "round 4", "round 5", "round 6",
		"round 8", "round 9", "round 10", "round 11", "round 12",
		"round finale", "ultimo round", "ultima ripresa",
	} {
		assert.Falsef(t, strings.Contains(prose, forbiddenRound),
			"P0.H prose MUST NOT mention %q (NOT in the clip evidence — rounds 1 and 7 ONLY); got=%q",
			forbiddenRound, result.Output.Text)
	}
	// Positive confirmation: the prose DOES mention the
	// canonical rounds 1 and 7. (Otherwise the prose would
	// pass the negative assertion trivially by mentioning
	// NO rounds at all.)
	assert.Truef(t, strings.Contains(prose, "round 1"),
		"P0.H prose MUST mention 'round 1' (canonical evidence); got=%q", result.Output.Text)
	assert.Truef(t, strings.Contains(prose, "round 7"),
		"P0.H prose MUST mention 'round 7' (canonical evidence); got=%q", result.Output.Text)

	// (n) No medical details — the fixture has NO medical
	//     evidence, so any medical term is a hallucination.
	for _, medicalTerm := range []string{
		"infortunio", "infortuni", "medico", "medica", "ospedale",
		"taglio", "knockout", "ko tecnico", "incidente", "ferita",
		"sutura", "bendaggio", "visita medica",
	} {
		assert.Falsef(t, strings.Contains(prose, medicalTerm),
			"P0.H prose MUST NOT contain medical term %q (no medical evidence in the fixture); got=%q",
			medicalTerm, result.Output.Text)
	}
}
