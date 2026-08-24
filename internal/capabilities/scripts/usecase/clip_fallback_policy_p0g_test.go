// Package scripts — clip_fallback_policy_p0g_test.go: P0.G
// fallback-policy test suite for /api/script/generate.
//
// July 2026 PR — P0.G gate (re-implementation). This file
// replaces the previous generate_fallback_e2e_test.go
// (commit 46386da94) with a fresh design: same spec, cleaner
// code, different file name + test names, and a
// vocabulary-engineering approach that makes the quality gate
// pass so the test reaches the ModeInfo assertions.
//
// USER-SPEC INVARIANTS — "stesso payload":
//
// Both scenarios MUST use the same payload (2 accepted clips) +
// the same engine output (1 scene, mismatch with the 2 clips).
// The ONLY difference between the two tests is the
// fallback_policy field. This is the cleanest way to surface
// the policy-branch divergence: 1 scene for 2 clips is
// "planner-non-valid" (counts don't match the 1:1 contract)
// and the second enforcement check in
// enforceClipNativeContract branches on policy.
//
//   - strict  → *ClipNativePlanningError{
//     Code: "CLIP_NATIVE_PLANNING_FAILED",
//     Policy: "strict"}
//   - allow_prose → warning appended + Status:
//     ItemStatusSucceededWithWarnings + ModeInfo flip
//
// Test seam: orchestrator-level (buildUsecaseWithClipResolver +
// fakeOllamaGen + fakeClipResolver). The default stub
// postprocessor does NOT set FinalSpecScene.Scenes, so the
// engine's 1 scene persists into enforceClipNativeContract —
// no binder override happens, no clip-native scene
// reconstruction. The clip resolver is registered with BOTH
// canonical clips so source resolution succeeds and the
// orchestrator reaches the enforce step.
//
// Vocabulary engineering for quality-gate isolation:
//
// Both item.Source.SourceText AND clip SearchText are set to
// p0gVocabulary, which is a SUPERSET of the prose's non-stop
// tokens. computeSourceTextCoverage returns ~1.0, well above
// the default 0.70 threshold. This isolates the fallback-policy
// contract from the source-text-coverage contract (the previous
// P0.G test failed at the quality gate BEFORE reaching the
// fallback-policy assertions, masking the actual contract under
// test).
//
// KNOWN GAP — allow_prose SUT bug (TDD-reveals-bug, same
// pattern as the original 46386da94 commit): the current
// production code (clip_native_enforce.go) declares
// usedMode = "clip_native" and fallbackUsed = false at the
// top of enforceClipNativeContract and NEVER mutates them in
// the policy-branchable mismatch block. So the allow_prose
// test WILL FAIL on current code at the ModeInfo assertions.
// The strict test passes today. The fix path is documented in
// the test header for the next contributor.
package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// p0gClipAlpha + p0gClipBeta are the canonical 2-clip IDs
// used by BOTH scenarios ("stesso payload" per user spec).
// Pinned as constants so a future contributor cannot
// accidentally switch to 1-clip or 3-clip variants and break
// the symmetric-mismatch invariant.
const (
	p0gClipAlpha = "clip-alpha"
	p0gClipBeta  = "clip-beta"
)

// p0gVocabulary is the SHARED source-text vocabulary assigned
// to BOTH item.Source.SourceText AND the clip SearchText. It
// is engineered to be a SUPERSET of the prose's non-stop-word
// tokens so computeSourceTextCoverage (which divides matches
// by generated-non-stop-tokens) is ~1.0, well above the
// default 0.70 threshold. This isolates the fallback-policy
// contract from the source-text-coverage contract.
//
// Coverage math (default policy, source threshold = 0.70):
//
//	prose non-stop tokens (after filterStopWords for the, a,
//	in, over, his, with, ...): ~26 unique
//	source non-stop tokens (in p0gVocabulary): ~35 unique
//	(SUPERSET of prose)
//	overlap / generated ≈ 26 / 26 ≈ 1.0  (≫ 0.70)
const p0gVocabulary = "The bell rings fighters touch gloves round 1 round 7 " +
	"boxing match action crowd cheers roars begins both trading " +
	"jabs challenger studies opponent carefully again trade more " +
	"quick brown fox jumps lazy dog intensifies ring great passion"

// p0gProse is the controlled ~54-word English narrative for
// the P0.G fixture. It uses the same vocabulary as
// p0gVocabulary so the quality gate's source-text coverage is
// ~1.0, and it is calibrated to TargetWords=50 (within the
// [40, 60] tolerance, per minTargetWordsRatio=0.80 and
// maxTargetWordsRatio=1.20 in quality_gate.go).
const p0gProse = "The bell rings. The fighters touch gloves. The crowd roars. " +
	"Round 1 begins with both fighters trading jabs. The challenger " +
	"studies his opponent carefully. The bell rings again. Round 7 " +
	"begins. The fighters trade more jabs. The quick brown fox jumps " +
	"over the lazy dog. The action intensifies in the ring with " +
	"great passion."

// p0gSingleSceneEnvelope is the canonical ModelScriptOutputV1
// scene with NO clip binding. Combined with the 2-clip
// request, this triggers the policy-branchable mismatch in
// enforceClipNativeContract (check 2: scene count != clip
// count, policy branches). The scene is intentionally bare
// (no clip binding) so the second enforcement check
// ("every accepted clip must be bound to a scene") ALSO fires
// — both checks compound the mismatch signal.
const p0gSingleSceneEnvelope = `{"id":"scene-1","index":0,"text":"S1.","kind":"narration","bindings":{}}`

// p0gBuildEnvelope returns the canonical ModelScriptOutputV1
// JSON envelope that, when decoded by the engine, yields
// engineResult.Output.SpecScene.Scenes with EXACTLY 1 entry —
// guaranteeing a mismatch with the 2-clip request payload.
func p0gBuildEnvelope(prose string) string {
	return fmt.Sprintf(
		`{"schema_version":1,"text":%q,"specscene":{"version":1,"scenes":[%s]}}`,
		prose, p0gSingleSceneEnvelope,
	)
}

// p0gBuildFallbackOrchestrator wires the canonical
// orchestrator (buildUsecaseWithClipResolver) with a
// fakeOllamaGen emitting the 1-scene envelope. The clip
// resolver is registered with BOTH canonical clips using the
// SHARED p0gVocabulary so source-text coverage is ~1.0.
// The default stub postprocessor in buildUsecaseWithClipResolver
// does NOT set FinalSpecScene.Scenes, so the engine's 1 scene
// propagates into enforceClipNativeContract as finalScenes = 1
// (vs clipIDs = 2 from effectiveClipIDs → mismatch → policy
// branch).
func p0gBuildFallbackOrchestrator(t *testing.T) (*GenerateOneUseCase, scriptpkg.GenerationItemV2) {
	t.Helper()

	gen := &fakeOllamaGen{result: &scriptports.GenerationResult{
		Script:      p0gBuildEnvelope(p0gProse),
		WordCount:   54,
		EstDuration: 3,
		Model:       "llama3:8b",
		Prompt:      "<p0g-test prompt — not asserted>",
	}}

	// Both clips carry the shared vocabulary so the resolver
	// combines a rich source-text into the plan, and the
	// quality gate's source-text coverage is ~1.0. The
	// 30-second duration is the canonical makeTestClip
	// duration so any downstream timing check passes.
	clip1 := makeTestClip(p0gClipAlpha, "Alpha clip", 30*time.Second)
	clip1.SearchText = p0gVocabulary
	clip2 := makeTestClip(p0gClipBeta, "Beta clip", 30*time.Second)
	clip2.SearchText = p0gVocabulary

	resolver := newFakeClipResolver()
	resolver.AddClip(clip1)
	resolver.AddClip(clip2)

	uc := buildUsecaseWithClipResolver(gen, resolver)

	item := makeClipsItem("p0g-fallback", []string{p0gClipAlpha, p0gClipBeta}, p0gVocabulary)
	item.ScriptParams.TargetWords = 50

	return uc, item
}

// TestFallbackPolicy_P0G_Strict_FailsOnMismatch pins scenario 1
// of the P0.G contract: fallback_policy = "strict" +
// planner-non-valid → the orchestrator returns the canonical
// typed *ClipNativePlanningError.
//
// "Job FAILED" + "NO prosa generica": the orchestrator
// propagates the error from Execute (matching the canonical
// source-resolution pattern from P0.C, which surfaces
// ErrSourceResolutionFailed via Execute — same propagation
// contract). The test asserts:
//   - Execute returns a non-nil error
//   - The error is the canonical typed ClipNativePlanningError
//   - Code = "CLIP_NATIVE_PLANNING_FAILED"
//   - Policy = "strict"
//   - The error message references the scene/clip count
//     mismatch (operator-dashboard grep-friendly)
//   - result == nil (orchestrator's error path short-circuits)
func TestFallbackPolicy_P0G_Strict_FailsOnMismatch(t *testing.T) {
	t.Parallel()

	uc, item := p0gBuildFallbackOrchestrator(t)
	item.Source.FallbackPolicy = scriptpkg.FallbackPolicyStrict

	result, err := uc.Execute(context.Background(), item, scriptpkg.Preset(""), nil)

	// (a) The job is FAILED via the typed-error path.
	require.Errorf(t, err,
		"strict policy + planner-invalid MUST surface an error from Execute (job FAILED, no prose generated); result=%+v",
		result)

	// (b) The error MUST be the canonical typed
	//     ClipNativePlanningError so handlers map it to a
	//     4xx-class response (canonical_errors.go convention).
	var planErr *scriptpkg.ClipNativePlanningError
	require.Truef(t, errors.As(err, &planErr),
		"strict policy error MUST be typed *ClipNativePlanningError (godlike/07 typed-error contract); got=%T err=%q",
		err, err.Error())

	// (c) Code MUST be the canonical "CLIP_NATIVE_PLANNING_FAILED"
	//     (this is the count-mismatch path, NOT the empty-scenes
	//     "CLIP_NATIVE_PLAN_UNAVAILABLE" path).
	assert.Equalf(t, "CLIP_NATIVE_PLANNING_FAILED", planErr.Code,
		"P0.G strict policy + 1-scene-vs-2-clips mismatch MUST surface as CLIP_NATIVE_PLANNING_FAILED; got=%q",
		planErr.Code)

	// (d) Policy MUST be "strict" (the active policy at the
	//     time of enforcement).
	assert.Equalf(t, scriptpkg.FallbackPolicyStrict, planErr.Policy,
		"P0.G strict policy MUST be recorded on the typed error; got=%q",
		planErr.Policy)

	// (e) Error message MUST surface the count mismatch
	//     (operator-dashboard grep-friendly invariant).
	assert.Truef(t,
		strings.Contains(err.Error(), "scene count") && strings.Contains(err.Error(), "clip count"),
		"P0.G strict error message MUST reference the scene-vs-clip count mismatch; got=%q",
		err.Error())

	// (f) "NO prosa generica" — the strict path MUST NOT
	//     degrade to prose. The canonical signal is: the
	//     orchestrator's error path short-circuits before
	//     result is populated, so Execute returns
	//     (nil, err) — matching the P0.C
	//     ErrSourceResolutionFailed propagation pattern.
	//     A future regression that degrades strict to
	//     prose would surface here as a non-nil result.
	assert.Nilf(t, result,
		"strict policy MUST NOT produce a GenerationResult (NO prose fallback); got=%+v", result)
}

// TestFallbackPolicy_P0G_AllowProse_SucceedsWithWarnings pins
// scenario 2 of the P0.G contract: fallback_policy =
// "allow_prose" + planner-non-valid → the orchestrator
// returns a non-nil GenerationResult with:
//
//   - Status = ItemStatusSucceededWithWarnings
//   - ModeInfo != nil
//   - ModeInfo.UsedMode = "prose"     (the fallback "prose" signal)
//   - ModeInfo.FallbackUsed = true    (the fallback-was-used boolean)
//   - Warnings non-empty (the planner failure reason is
//     surfaced to the operator dashboard for visibility)
//
// Same payload as the strict scenario (2 clips, 1 scene) — the
// ONLY difference is item.Source.FallbackPolicy = "allow_prose".
func TestFallbackPolicy_P0G_AllowProse_SucceedsWithWarnings(t *testing.T) {
	t.Parallel()

	uc, item := p0gBuildFallbackOrchestrator(t)
	item.Source.FallbackPolicy = scriptpkg.FallbackPolicyAllowProse

	result, err := uc.Execute(context.Background(), item, scriptpkg.Preset(""), nil)

	// (a) No error — the orchestrator degrades to prose
	//     (per user spec: "status=SUCCEEDED_WITH_WARNINGS",
	//     not a hard failure).
	require.NoErrorf(t, err,
		"allow_prose policy + planner-invalid MUST NOT surface as a hard error; the orchestrator degrades to prose; err=%v",
		err)
	require.NotNilf(t, result,
		"allow_prose policy MUST return a non-nil GenerationResult (the prose-fallback surface); got=nil")

	// (b) Status MUST be ItemStatusSucceededWithWarnings —
	//     the canonical "degraded to prose" status, distinct
	//     from the full-success Status=ItemStatusSucceeded.
	assert.Equalf(t, scriptpkg.ItemStatusSucceededWithWarnings, result.Status,
		"P0.G allow_prose policy MUST set Status=ItemStatusSucceededWithWarnings (degraded surface); got=%q",
		result.Status)

	// (c) ModeInfo MUST be non-nil and reflect the prose
	//     fallback.
	require.NotNilf(t, result.ModeInfo,
		"P0.G allow_prose policy MUST populate result.ModeInfo (the canonical 'we degraded' signal); got=nil")
	assert.Equalf(t, "prose", result.ModeInfo.UsedMode,
		"P0.G allow_prose policy MUST set ModeInfo.UsedMode=\"prose\" (the canonical prose-fallback marker); got=%q",
		result.ModeInfo.UsedMode)
	assert.Truef(t, result.ModeInfo.FallbackUsed,
		"P0.G allow_prose policy MUST set ModeInfo.FallbackUsed=true (the canonical fallback-was-used boolean); got=false")

	// (d) Warnings MUST surface the planner failure reason
	//     (operator-dashboard visibility for the "we
	//     degraded" event).
	require.NotEmptyf(t, result.Warnings,
		"P0.G allow_prose policy MUST surface the planner-failure reason in result.Warnings (operator-dashboard visibility); got=empty")
	warningsBlob := strings.Join(result.Warnings, " | ")
	assert.Truef(t,
		strings.Contains(warningsBlob, "CLIP_NATIVE_PLAN_UNAVAILABLE") ||
			strings.Contains(warningsBlob, "scene count") ||
			strings.Contains(warningsBlob, "clip count"),
		"P0.G allow_prose warnings MUST reference the planner-failure reason (CLIP_NATIVE_PLAN_UNAVAILABLE / scene-vs-clip count); got=%q",
		warningsBlob)

	// (e) Loud KNOWN GAP marker — the current production
	//     code (clip_native_enforce.go) declares
	//     usedMode = "clip_native" and fallbackUsed = false
	//     at the top of enforceClipNativeContract and never
	//     mutates them in the mismatch path. So this test
	//     WILL FAIL on current code. The CI failure
	//     surfaces the production bug for the next
	//     contributor to fix.
	t.Logf("P0.G_KNOWN_GAP_ALLOW_PROSE_MODE_INFO: scenario=allow_prose+failure status=%q usedMode=%q fallbackUsed=%v (CURRENT EXPECTED: status=SUCCEEDED_WITH_WARNINGS usedMode=prose fallbackUsed=true — production code may NOT yet flip the ModeInfo flags; if usedMode != 'prose' the production fix is incomplete)",
		result.Status, result.ModeInfo.UsedMode, result.ModeInfo.FallbackUsed)
}
