package scriptgeneration

// runner_overlay_plan_branch_test.go pins the three P1 guarantees:
//
//  1. (end-to-end) a full COMBINED_TIMELINE run still completes with a
//     certified overlay plan when the projection was produced by the branch
//     and applied at the join — the audio phase runs the branch by default
//     on this path.
//  2. (branch machinery) the detached branch projection is byte-equal — per
//     field — to the serial SSOT projection computeOverlayPlanProjection runs
//     on the same state, including the branch's own scene resolution and
//     canonical timeline derivation.
//  3. (join) the join is idempotent: replaying the serial SSOT projection on
//     a stripped copy reproduces the branch-applied plan byte for byte.

import (
	"context"
	"testing"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/stretchr/testify/require"
)

// overlayPlanBranchScenes returns the overlay-cert fixture: real semantic
// annotations on both scenes so the compiled plan is non-nil and every
// projected field is observable.
func overlayPlanBranchScenes() []Scene {
	return []Scene{
		{
			ID: "scene-0", Index: 0,
			Text:        map[Language]string{"en": "Tim Cook said that Apple changed everything in Cupertino and sold ten million Vision Pro units in 2025 on March 5, 2026, growing 25% with $2 million in revenue."},
			Annotations: overlayScene0Annotations(),
			Audio:       capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover},
		},
		{
			ID: "scene-1", Index: 1,
			Text:        map[Language]string{"en": "Growth matters more than ever."},
			Annotations: overlayScene1Annotations(),
			Audio:       capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover},
		},
	}
}

func overlayPlanBranchRequest() GenerateRequest {
	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeCombinedTimeline
	req.Source.Type = SourceText
	req.Languages = []Language{"en"}
	req.Project = "overlay-plan-branch-cert"
	req.AnimationCounts = map[string]int{"chain_disabled": 1}
	return req
}

// runOverlayPlanBranchRun executes one full COMBINED_TIMELINE run on the
// overlay-cert fixture and returns the runner together with the durable result.
func runOverlayPlanBranchRun(t *testing.T, runID string) (*Runner, GenerateRequest, *GenerateResult) {
	t.Helper()
	repo := newInMemRunRepository()
	voiceoverGen := &blockingTimingVoiceoverGenerator{release: make(chan struct{})}
	runner := NewRunner(repo, newStubTextGenerator(overlayPlanBranchScenes()), newStubTranslator(), voiceoverGen)
	runner.SetCombinedAudioRenderer(&stubCombinedAudioRenderer{})
	runner.SetOverlayCanvas(GoldenOverlayCanvas)

	req := overlayPlanBranchRequest()
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))
	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.Execute(context.Background(), runID, req)
	}()
	close(voiceoverGen.release)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("run did not complete")
	}
	final := awaitCompletion(t, repo, runID, time.Second)
	require.Equal(t, RunStatusCompleted, final.Status, "run must complete: %s", final.ErrorMessage)
	require.NotNil(t, final.Result, "durable result must be present")
	return runner, req, final.Result
}

// projectionTestRunner wires a runner with the same canvas/plugins state the
// full-run fixture uses, without executing a pipeline.
func projectionTestRunner() *Runner {
	runner := NewRunner(newInMemRunRepository(), newStubTextGenerator(overlayPlanBranchScenes()), newStubTranslator(), &blockingTimingVoiceoverGenerator{release: make(chan struct{})})
	runner.SetCombinedAudioRenderer(&stubCombinedAudioRenderer{})
	runner.SetOverlayCanvas(GoldenOverlayCanvas)
	return runner
}

// TestOverlayPlanBranch_EndToEndAppliesBranchProjection pins guarantee 1: the
// durable result carries every projected field the branch joined, on a real
// completed run.
func TestOverlayPlanBranch_EndToEndAppliesBranchProjection(t *testing.T) {
	_, _, res := runOverlayPlanBranchRun(t, "run-overlay-plan-branch-e2e")
	require.NotNil(t, res.OverlayPlan, "the branch-produced overlay plan must reach the durable result")
	require.NotNil(t, res.OverlayPlanIntake, "the branch-produced plan intake must reach the durable result")
	require.NotNil(t, res.PhraseTimings, "the branch-produced phrase timings must reach the durable result")
	require.NotNil(t, res.SceneSpeechTimings, "the branch-produced scene speech timings must reach the durable result")
	require.NotNil(t, res.EntityTimeline, "the branch-produced entity timeline must reach the durable result")
	require.NotNil(t, res.PhraseOverlayBudget, "the branch-produced phrase overlay budget must reach the durable result")
	require.Greater(t, len(res.OverlayPlan.Items), 0, "the branch-produced plan must carry overlay items")
}

// TestOverlayPlanBranch_ProjectionEquivalence pins guarantee 2: on the same
// stripped certified state, the serial SSOT projection and the detached branch
// projection (which re-derives resolved scenes and the canonical timeline)
// produce field-identical results.
func TestOverlayPlanBranch_ProjectionEquivalence(t *testing.T) {
	_, req, res := runOverlayPlanBranchRun(t, "run-overlay-plan-branch-equivalence")
	require.NotNil(t, res.OverlayPlan, "the branch-produced overlay plan must reach the durable result")

	strip := func() *GenerateResult {
		snapshot, err := snapshotGenerateResult(res)
		require.NoError(t, err)
		snapshot.ResolvedScenes = nil
		snapshot.CanonicalTimeline = nil
		return snapshot
	}

	serialInput := strip()
	require.NoError(t, projectionTestRunner().computeOverlayPlanProjection(context.Background(), "run-overlay-plan-branch-equivalence", req, "", serialInput))

	branchInput := strip()
	require.NoError(t, projectionTestRunner().computeOverlayPlanBranchProjection(context.Background(), "run-overlay-plan-branch-equivalence", req, "", capabilityaudio.AudioModeCombinedTimeline, branchInput))

	require.Equal(t, serialInput.PhraseTimings, branchInput.PhraseTimings, "phrase timings must be field-identical between the serial and detached projections")
	require.Equal(t, serialInput.SceneSpeechTimings, branchInput.SceneSpeechTimings, "scene speech timings must be field-identical")
	require.Equal(t, serialInput.EntityTimeline, branchInput.EntityTimeline, "entity timeline must be field-identical")
	require.Equal(t, serialInput.OverlayPlan, branchInput.OverlayPlan, "overlay plan must be field-identical")
	require.Equal(t, serialInput.OverlayPlanIntake, branchInput.OverlayPlanIntake, "plan intake telemetry must be field-identical")
	require.Equal(t, serialInput.PhraseOverlayBudget, branchInput.PhraseOverlayBudget, "phrase overlay budget must be field-identical")
	require.Equal(t, serialInput.LocalizedOverlayPlans, branchInput.LocalizedOverlayPlans, "localized plans must be field-identical")
	require.Equal(t, serialInput.SemanticRenderBundle, branchInput.SemanticRenderBundle, "semantic render bundle must be field-identical")
	require.Equal(t, serialInput.ResolvedOverlayIntents, branchInput.ResolvedOverlayIntents, "resolved overlay intents must be field-identical")
}

// TestOverlayPlanBranch_JoinIsIdempotent pins guarantee 3: replaying the
// serial SSOT projection on a stripped copy of the applied result reproduces
// the branch-applied plan byte for byte.
func TestOverlayPlanBranch_JoinIsIdempotent(t *testing.T) {
	_, req, res := runOverlayPlanBranchRun(t, "run-overlay-plan-branch-idempotent-join")
	require.NotNil(t, res.OverlayPlan)

	stripped, err := snapshotGenerateResult(res)
	require.NoError(t, err)
	stripped.ResolvedScenes = nil
	stripped.CanonicalTimeline = nil
	// The join asserted the real broker job identity on the applied plan; the
	// replay must reassert the same ID so the comparison stays on plan facts.
	replayJobID := res.OverlayPlan.DriveJobID
	require.NoError(t, projectionTestRunner().computeOverlayPlanProjection(context.Background(), "run-overlay-plan-branch-idempotent-join", req, replayJobID, stripped))
	require.Equal(t, res.OverlayPlan, stripped.OverlayPlan, "the serial projection must reproduce the branch-applied plan")
}
