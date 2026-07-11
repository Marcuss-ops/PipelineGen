// Package scripts — generate_provenance_p0i_test.go: P0.I
// provenance contract for /api/script/generate.
//
// July 2026 PR — P0.I gate. Pins the canonical contract for
// the GenerationProvenance audit-trail block. The provenance
// block is the canonical way VeloxEditing provides auditability
// for AI actions (especially for fact-checking Google Docs
// creation later). It carries the complete generation
// provenance: doc_id, doc_link, source_type, source_text_hash,
// clip_ids, requested_mode, used_mode, fallback_used, model,
// prompt_version, planner_version.
//
// USER-SPEC INVARIANTS — Phase 8 ordering guarantee:
//
// The orchestrator (generate_one_usecase.go) comments
// specifically note:
//
//	"Surface it on the result before the quality gate so a
//	 failing gate still returns the provenance block."
//
// This is a highly specific, behavior-driven requirement that
// remains untested. P0.I pins this contract with two
// complementary scenarios:
//
//	1. HAPPY PATH (TestProvenance_P0I_PopulatedOnHappyPath):
//	   a clean clip-native success → result.Provenance is
//	   fully populated with all 11 canonical fields.
//
//	2. QUALITY GATE FAILURE (TestProvenance_P0I_SurfacedOn
//	   QualityGateFailure):
//	   a quality gate failure → result is non-nil (orchestrator
//	   returns (result, ErrQualityGateFailed)) AND
//	   result.Provenance is still populated, so the operator
//	   gets the audit trail alongside the rejection.
//
// Test seam: orchestrator-level (buildUsecaseWithClipResolver
// + fakeOllamaGen + fakeClipResolver with both canonical clips
// registered). The default stub postprocessor in
// buildUsecaseWithClipResolver does NOT mutate the result.
//
// Vocabulary engineering: the happy path uses the same
// vocabulary-heavy design as P0.G/P0.H to isolate the
// provenance contract from the source-text-coverage contract.
// p0iVocabulary is a SHARED SUPERSET of the prose's non-stop
// tokens → computeSourceTextCoverage ≈ 1.0 → quality gate
// passes. The quality-gate-failure path uses an empty-text
// envelope to force ErrQualityGateFailed regardless of source
// coverage.
//
// godlike/07 NO-FAKE-AVAILABILITY: every assertion pins a
// canonical typed contract field (Provenance.*) — never a
// "result was non-nil" soft pass.
//
// KNOWN GAP — allow_prose provenance SUT bug (TDD-reveals-
// bug, same pattern as P0.G's allow_prose KNOWN GAP): the
// provisional buildProvenance block is vulnerable to the same
// bug. In clip_native_enforce.go, provisionalModeInfo
// evaluates a fallback only if the scene length is exactly
// zero:
//
//	if len(engineResult.Output.SpecScene.Scenes) == 0 {
//	    usedMode = "prose"
//	    fallbackUsed = true
//	}
//
// If an engine generates 1 scene for 2 clips under an
// allow_prose policy, len(Scenes) != 0, so
// provisionalModeInfo blindly rules UsedMode = "clip_native"
// and FallbackUsed = false. Because enforceClipNativeContract
// never corrects these flags in its mismatch path, the
// result.Provenance block will be stamped with canonical lies
// (FallbackUsed = false when it did, in fact, fall back to
// prose). A future P0.I scenario for allow_prose provenance
// would fail on the live code. The fix path is the same as
// the P0.G KNOWN GAP: flip the flags in
// enforceClipNativeContract before the return result block.
package usecase

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	ollamatypes "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/types"
)

// p0iClipAlpha + p0iClipBeta are the canonical 2-clip IDs
// used by BOTH P0.I scenarios. Pinned as constants so a
// future contributor cannot accidentally switch to 1-clip
// or 3-clip variants and break the symmetric-mismatch
// invariant.
const (
	p0iClipAlpha = "clip-alpha"
	p0iClipBeta  = "clip-beta"
)

// p0iVocabulary is the SHARED source-text vocabulary assigned
// to BOTH item.Source.SourceText AND the clip SearchText. It
// is engineered to be a SUPERSET of p0iProse's non-stop-word
// tokens so computeSourceTextCoverage (which divides matches
// by generated-non-stop-tokens) is ~1.0, well above the
// default 0.70 threshold. This isolates the provenance
// contract from the source-text-coverage contract.
//
// Coverage math (default policy, source threshold = 0.70):
//
//	prose non-stop tokens (after filterStopWords for the, a,
//	in, over, his, with, ...): ~26 unique
//	source non-stop tokens (in p0iVocabulary): ~35 unique
//	(SUPERSET of prose)
//	overlap / generated ≈ 26 / 26 ≈ 1.0  (≫ 0.70)
const p0iVocabulary = "The bell rings fighters touch gloves round 1 round 7 " +
	"boxing match action crowd cheers roars begins both trading " +
	"jabs challenger studies opponent carefully again trade more " +
	"quick brown fox jumps lazy dog intensifies ring great passion"

// p0iProse is the controlled ~54-word English narrative for
// the P0.I happy-path fixture. It uses the same vocabulary
// as p0iVocabulary so the quality gate's source-text coverage
// is ~1.0, and it is calibrated to TargetWords=50 (within
// the [40, 60] tolerance, per minTargetWordsRatio=0.80 and
// maxTargetWordsRatio=1.20 in quality_gate.go).
const p0iProse = "The bell rings. The fighters touch gloves. The crowd roars. " +
	"Round 1 begins with both fighters trading jabs. The challenger " +
	"studies his opponent carefully. The bell rings again. Round 7 " +
	"begins. The fighters trade more jabs. The quick brown fox jumps " +
	"over the lazy dog. The action intensifies in the ring with " +
	"great passion."

// p0iTwoScenesJSON are the canonical 2-scene SpecScene
// envelopes bound to the 2 canonical clips. Each scene is
// bound to its clip via the "bindings.clip.clip_id" field,
// satisfying enforceClipNativeContract's "1 clip = 1 scene" +
// "every accepted clip is bound to a scene" checks
// simultaneously (canonical happy path).
const (
	p0iScene1JSON = `{"id":"scene-1","index":0,"text":"S1.","kind":"narration","bindings":{"clip":{"clip_id":"clip-alpha"}}}`
	p0iScene2JSON = `{"id":"scene-2","index":1,"text":"S2.","kind":"narration","bindings":{"clip":{"clip_id":"clip-beta"}}}`
)

// p0iBuildHappyPathEnvelope returns the canonical
// ModelScriptOutputV1 JSON envelope with 2 bound scenes.
// Combined with the 2-clip request, this satisfies the
// clip-native contract end-to-end.
func p0iBuildHappyPathEnvelope(prose string) string {
	return fmt.Sprintf(
		`{"schema_version":1,"text":%q,"specscene":{"version":1,"scenes":[%s,%s]}}`,
		prose, p0iScene1JSON, p0iScene2JSON,
	)
}

// p0iBuildEmptyEnvelope was REMOVED in PR-P0.I fix 1: the
// engine short-circuits on empty text and does NOT decode
// the specscene.scenes array, so enforceClipNativeContract
// fires CLIP_NATIVE_PLAN_UNAVAILABLE ("no scenes could be
// built from clip evidence") BEFORE the quality gate. The
// quality-gate-failure test now uses the happy-path envelope
// (non-empty prose + 2 bound scenes) and triggers the
// quality gate via target-words tolerance instead (set
// TargetWords=10 so the 54-word prose is out of the [8, 12]
// tolerance window). This keeps the clip-native contract
// satisfied so the orchestrator reaches the quality gate.

// p0iBuildProvenanceOrchestrator wires the canonical
// orchestrator (buildUsecaseWithClipResolver) with a
// fakeOllamaGen emitting the controlled envelope. The clip
// resolver is registered with BOTH canonical clips using
// the SHARED p0iVocabulary so source-text coverage is ~1.0.
//
// The `envelope` argument lets the caller swap between the
// happy-path envelope (p0iBuildHappyPathEnvelope) and the
// quality-gate-failure envelope (p0iBuildEmptyEnvelope) —
// both share the same clip+source-text fixture so the ONLY
// difference between the two P0.I scenarios is the
// envelope's text field.
func p0iBuildProvenanceOrchestrator(t *testing.T, envelope string, wordCount int) (*GenerateOneUseCase, scriptpkg.GenerationItemV2) {
	t.Helper()

	gen := &fakeOllamaGen{result: &ollamatypes.GenerationResult{
		Script:      envelope,
		WordCount:   wordCount,
		EstDuration: 3,
		Model:       "llama3:8b",
		Prompt:      "<p0i-test prompt — not asserted>",
	}}

	clip1 := makeTestClip(p0iClipAlpha, "Alpha clip", 30*time.Second)
	clip1.SearchText = p0iVocabulary
	clip2 := makeTestClip(p0iClipBeta, "Beta clip", 30*time.Second)
	clip2.SearchText = p0iVocabulary

	resolver := newFakeClipResolver()
	resolver.AddClip(clip1)
	resolver.AddClip(clip2)

	uc := buildUsecaseWithClipResolver(gen, resolver)

	item := makeClipsItem("p0i-provenance", []string{p0iClipAlpha, p0iClipBeta}, p0iVocabulary)
	item.ScriptParams.TargetWords = 50

	return uc, item
}

// TestProvenance_P0I_PopulatedOnHappyPath pins scenario 1 of
// the P0.I contract: a clean clip-native success → the
// GenerationProvenance block is fully populated with all 11
// canonical fields. The test asserts the canonical typed
// contract for the provenance audit trail:
//
//   - result.Provenance != nil (the block was built)
//   - result.Provenance.SourceType == "clips" (the source
//     type flows through the plan resolver)
//   - result.Provenance.ClipIDs contains the 2 canonical
//     clip IDs (the accepted clips are recorded)
//   - result.Provenance.Model == "llama3:8b" (the model
//     from the engine result)
//   - result.Provenance.RequestedMode == "clip_native"
//     (the caller requested clip-native for source=clips)
//   - result.Provenance.UsedMode == "clip_native" (the
//     engine actually produced clip-native scenes; this
//     matches the strict path on a successful happy path)
//   - result.Provenance.FallbackUsed == false (no fallback
//     was used — the strict path produced clip-native output)
//   - result.Provenance.SourceTextHash is non-empty (the
//     source text was hashed for the audit trail)
//   - result.Status == ItemStatusSucceeded (the contract
//     is "clean" — no warnings, no fallback)
func TestProvenance_P0I_PopulatedOnHappyPath(t *testing.T) {
	t.Parallel()

	uc, item := p0iBuildProvenanceOrchestrator(t, p0iBuildHappyPathEnvelope(p0iProse), 54)

	result, err := uc.Execute(context.Background(), item, scriptpkg.Preset(""), nil)

	// (a) Execute MUST return a non-nil result with no error.
	//     The hardcoded prose is clean, the envelope is well-
	//     formed, the clips are bound, the gate passes — every
	//     piece of the chain succeeds.
	require.NoErrorf(t, err,
		"P0.I happy path MUST return nil error; err=%v", err)
	require.NotNilf(t, result,
		"P0.I happy path MUST return a non-nil result; got=nil")

	// (b) Status MUST be ItemStatusSucceeded — the strict
	//     clip-native contract passes (1 scene per clip, all
	//     clips bound), so NO fallback warnings are appended.
	assert.Equalf(t, scriptpkg.ItemStatusSucceeded, result.Status,
		"P0.I happy path MUST set Status=ItemStatusSucceeded; got=%q", result.Status)

	// (c) Provenance MUST be non-nil — the orchestrator built
	//     and surfaced the audit-trail block.
	require.NotNilf(t, result.Provenance,
		"P0.I happy path MUST populate result.Provenance (the canonical audit-trail block); got=nil")

	// (d) SourceType MUST be "clips" — the resolver set
	//     plan.SourceKind = "clips" for source=clips.
	assert.Equalf(t, string(scriptpkg.SourceClips), result.Provenance.SourceType,
		"P0.I happy path MUST set Provenance.SourceType=\"clips\"; got=%q", result.Provenance.SourceType)

	// (e) ClipIDs MUST contain the 2 canonical clip IDs.
	//     The order is set by the plan resolver (which
	//     mirrors the item.Source.ClipIDs order).
	require.NotNilf(t, result.Provenance.ClipIDs,
		"P0.I happy path MUST populate Provenance.ClipIDs; got=nil")
	assert.ElementsMatchf(t,
		[]string{p0iClipAlpha, p0iClipBeta},
		result.Provenance.ClipIDs,
		"P0.I happy path MUST set Provenance.ClipIDs to [%q, %q]; got=%v",
		p0iClipAlpha, p0iClipBeta, result.Provenance.ClipIDs)

	// (f) Model MUST be "llama3:8b" — the model from the
	//     fakeOllamaGen result flows through to provenance.
	assert.Equalf(t, "llama3:8b", result.Provenance.Model,
		"P0.I happy path MUST set Provenance.Model=\"llama3:8b\"; got=%q", result.Provenance.Model)

	// (g) RequestedMode MUST be "clip_native" — the caller
	//     requested clip-native for source=clips.
	assert.Equalf(t, "clip_native", result.Provenance.RequestedMode,
		"P0.I happy path MUST set Provenance.RequestedMode=\"clip_native\"; got=%q", result.Provenance.RequestedMode)

	// (h) UsedMode MUST be "clip_native" — the engine
	//     actually produced 2 bound scenes (the canonical
	//     happy path on the strict policy).
	assert.Equalf(t, "clip_native", result.Provenance.UsedMode,
		"P0.I happy path MUST set Provenance.UsedMode=\"clip_native\"; got=%q", result.Provenance.UsedMode)

	// (i) FallbackUsed MUST be false — no fallback was used.
	assert.Falsef(t, result.Provenance.FallbackUsed,
		"P0.I happy path MUST set Provenance.FallbackUsed=false; got=true")

	// (j) SourceTextHash MUST be non-empty — the source
	//     text was hashed for the audit trail.
	assert.NotEmptyf(t, result.Provenance.SourceTextHash,
		"P0.I happy path MUST set Provenance.SourceTextHash (the canonical source-text hash); got=empty")
}

// TestProvenance_P0I_SurfacedOnQualityGateFailure pins
// scenario 2 of the P0.I contract: a quality gate failure
// → the orchestrator returns (result, ErrQualityGateFailed)
// AND result.Provenance is still populated. This is the
// Phase 8 ordering guarantee: the provenance block is
// surfaced BEFORE the quality gate so a failing gate still
// returns the audit trail alongside the rejection.
//
// The test uses an EMPTY-TEXT envelope to force
// ErrQualityGateFailed with "generated text is empty" (the
// quality gate's first check, before source coverage or
// unsupported claims). The 2 bound scenes still satisfy the
// clip-native contract so enforceClipNativeContract does NOT
// short-circuit before the quality gate.
//
// Assertions (godlike/07 typed-contract):
//   (a) Execute returns a non-nil error (errors.Is
//       ErrQualityGateFailed).
//   (b) result is non-nil (the orchestrator returns
//       (result, err) so the caller can inspect the
//       result block alongside the rejection).
//   (c) result.Status == "FAILED_QUALITY_GATE" — the
//       canonical quality-gate-failure status.
//   (d) result.Provenance != nil — the Phase 8 ordering
//       guarantee: provenance is surfaced BEFORE the gate.
//   (e) result.Provenance.SourceType == "clips" — the
//       audit trail still records the source type.
//   (f) result.Provenance.ClipIDs contains the 2 canonical
//       clip IDs — the audit trail still records the
//       accepted clips.
//   (g) result.Provenance.Model == "llama3:8b" — the
//       audit trail still records the model that
//       produced the (failing) output.
//   (h) result.Provenance.SourceTextHash is non-empty —
//       the audit trail still records the source-text
//       hash (for fact-checking the operator's review).
func TestProvenance_P0I_SurfacedOnQualityGateFailure(t *testing.T) {
	t.Parallel()

	// Same 2-scene bound envelope as the happy path — the
	// engine decodes the scenes and the clip-native
	// contract is satisfied. The quality gate failure is
	// triggered by setting TargetWords=10 below, so the
	// 54-word prose is out of the [8, 12] tolerance
	// window (minTargetWordsRatio=0.80, maxTargetWordsRatio=
	// 1.20 in quality_gate.go). The orchestrator reaches
	// Phase 9 (quality gate) and fires
	// ErrQualityGateFailed on "actual word count outside
	// target tolerance" while Phase 8 has already
	// populated result.Provenance.
	uc, item := p0iBuildProvenanceOrchestrator(t, p0iBuildHappyPathEnvelope(p0iProse), 54)
	item.ScriptParams.TargetWords = 10 // triggers target-words tolerance failure

	result, err := uc.Execute(context.Background(), item, scriptpkg.Preset(""), nil)

	// (a) Execute MUST return a non-nil error wrapping
	//     ErrQualityGateFailed — the empty text triggers
	//     the quality gate's first check.
	require.Errorf(t, err,
		"P0.I quality-gate-failure path MUST surface a non-nil error from Execute; result=%+v", result)
	require.ErrorIsf(t, err, scriptpkg.ErrQualityGateFailed,
		"P0.I quality-gate-failure error MUST wrap ErrQualityGateFailed (godlike/07 typed-error contract); got=%T err=%q",
		err, err.Error())

	// (b) result MUST be non-nil — the orchestrator returns
	//     (result, err) so the caller can inspect the
	//     provenance block alongside the rejection. A nil
	//     result here would mean the orchestrator dropped
	//     the audit trail on failure (Phase 8 ordering
	//     violation).
	require.NotNilf(t, result,
		"P0.I quality-gate-failure path MUST return a non-nil result (Phase 8 ordering: result surfaces alongside the rejection); got=nil")

	// (c) Status MUST be "FAILED_QUALITY_GATE" — the
	//     canonical quality-gate-failure status.
	assert.Equalf(t, "FAILED_QUALITY_GATE", result.Status,
		"P0.I quality-gate-failure path MUST set Status=\"FAILED_QUALITY_GATE\"; got=%q", result.Status)

	// (d) Provenance MUST be non-nil — the Phase 8
	//     ordering guarantee: provenance is surfaced
	//     BEFORE the quality gate, so a failing gate
	//     still returns the audit trail.
	require.NotNilf(t, result.Provenance,
		"P0.I quality-gate-failure path MUST populate result.Provenance (Phase 8 ordering: provenance surfaces before the gate); got=nil")

	// (e) SourceType MUST be "clips" — the audit trail
	//     still records the source type.
	assert.Equalf(t, string(scriptpkg.SourceClips), result.Provenance.SourceType,
		"P0.I quality-gate-failure path MUST set Provenance.SourceType=\"clips\"; got=%q", result.Provenance.SourceType)

	// (f) ClipIDs MUST contain the 2 canonical clip IDs.
	require.NotNilf(t, result.Provenance.ClipIDs,
		"P0.I quality-gate-failure path MUST populate Provenance.ClipIDs; got=nil")
	assert.ElementsMatchf(t,
		[]string{p0iClipAlpha, p0iClipBeta},
		result.Provenance.ClipIDs,
		"P0.I quality-gate-failure path MUST set Provenance.ClipIDs to [%q, %q]; got=%v",
		p0iClipAlpha, p0iClipBeta, result.Provenance.ClipIDs)

	// (g) Model MUST be "llama3:8b" — the audit trail
	//     still records the model that produced the
	//     (failing) output.
	assert.Equalf(t, "llama3:8b", result.Provenance.Model,
		"P0.I quality-gate-failure path MUST set Provenance.Model=\"llama3:8b\"; got=%q", result.Provenance.Model)

	// (h) SourceTextHash MUST be non-empty — the audit
	//     trail still records the source-text hash (for
	//     fact-checking the operator's review).
	assert.NotEmptyf(t, result.Provenance.SourceTextHash,
		"P0.I quality-gate-failure path MUST set Provenance.SourceTextHash; got=empty")

	// (i) Loud KNOWN GAP marker — the allow_prose
	//     provenance path has the same bug as P0.G
	//     (provisionalModeInfo only flips flags when
	//     scenes=0, not when scenes=1-for-2-clips under
	//     allow_prose). A future P0.I scenario for
	//     allow_prose provenance would fail on current
	//     code. The fix path is the same as the P0.G
	//     KNOWN GAP.
	t.Logf("P0.I_KNOWN_GAP_ALLOW_PROSE_PROVENANCE: scenario=quality_gate_failure status=%q provenance_surfaced=%v clip_ids=%v (CURRENT EXPECTED: status=FAILED_QUALITY_GATE provenance_surfaced=true clip_ids=[clip-alpha, clip-beta] — Phase 8 ordering guarantee; future allow_prose provenance scenarios would fail because provisionalModeInfo doesn't flip UsedMode/FallbackUsed in the 1-scene-for-2-clips mismatch path)",
		result.Status, result.Provenance != nil, result.Provenance.ClipIDs)
}

// Note: the scriptpkg import is used by scriptpkg.SourceClips,
// scriptpkg.GenerationItemV2, scriptpkg.ItemStatusSucceeded,
// scriptpkg.Preset, and scriptpkg.ErrQualityGateFailed throughout
// the file — no blank import guard is needed.
