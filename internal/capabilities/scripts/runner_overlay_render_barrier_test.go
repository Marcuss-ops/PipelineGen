package scriptgeneration

// runner_overlay_render_barrier_test.go pins the P2 submit-barrier guarantees:
//
//  1. (early submit) on the overlap path with the gate on, the barrier renders
//     the pending plans off the detached P1 outcome — provably before the fork
//     could claim them, with the master audio render released early so any
//     non-barrier submission would have taken the fork path instead.
//  2. (exactly once) a language the barrier certified is never re-submitted by
//     the OnPlanReady fork: after the join the enqueuer saw one call per
//     language and the durable result carries the references.
//  3. (fail-closed surface) a barrier render error never fails the run from the
//     barrier's own goroutine: the fork re-renders the unclaimed language and
//     the run fails exactly like today's overlay failure does.
//  4. (no authority) the barrier is nil for final jobs, serial render callers,
//     missing enqueuers, disabled renders and resumed-past stages; a failing
//     plan branch submits nothing and claims nothing.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// barrierOverlayTestRenderer is the barrier-test enqueuer: unlike the P1
// fixture's renderer it survives MULTIPLE calls (the barrier's first, the
// fork's re-render of what the barrier failed) and closes its channels exactly
// once.
type barrierOverlayTestRenderer struct {
	started     chan struct{}
	startedOnce sync.Once
	release     chan struct{}
	drained     chan struct{}
	drainedOnce sync.Once
	err         error
	calls       atomic.Int32
}

func newBarrierOverlayTestRenderer() *barrierOverlayTestRenderer {
	return &barrierOverlayTestRenderer{
		started: make(chan struct{}),
		release: make(chan struct{}),
		drained: make(chan struct{}),
	}
}

func (r *barrierOverlayTestRenderer) EnqueueChrononPlan(ctx context.Context, plan capabilityoverlay.OverlayPlan) (RenderReference, error) {
	r.calls.Add(1)
	r.startedOnce.Do(func() { close(r.started) })
	select {
	case <-ctx.Done():
		return RenderReference{}, ctx.Err()
	case <-r.release:
	}
	r.drainedOnce.Do(func() { close(r.drained) })
	if r.err != nil {
		return RenderReference{}, r.err
	}
	return RenderReference{JobID: plan.PlanID, Status: "COMPLETED"}, nil
}

// newOverlayBarrierFixture is newAudioOverlapFixture with the barrier-test
// enqueuer: same master audio renderer, same run, same single 'en' scene.
func newOverlayBarrierFixture(t *testing.T) (*executionRun, *audioJoinRepository, *barrierAudioRenderer, *barrierOverlayTestRenderer) {
	t.Helper()
	repo := &audioJoinRepository{inMemRunRepository: newInMemRunRepository()}
	runner := NewRunner(repo, newStubTextGenerator(nil), newStubTranslator(), &entityTimelineVoiceoverGenerator{})
	runner.SetLogger(zap.NewNop())
	audio := &barrierAudioRenderer{started: make(chan struct{}), release: make(chan struct{}), drained: make(chan struct{})}
	overlay := newBarrierOverlayTestRenderer()
	runner.SetCombinedAudioRenderer(audio)
	runner.SetOverlayRenderEnqueuer(overlay)
	runner.SetOverlayCanvas(GoldenOverlayCanvas)
	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeCombinedTimeline
	req.Languages = []Language{"en"}
	req.Render.Enabled = true
	scene := Scene{ID: "scene-0", Index: 0, Text: map[Language]string{"en": "Growth matters more than ever."}, Annotations: overlayScene1Annotations(), Audio: capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover}}
	vo, err := (&entityTimelineVoiceoverGenerator{}).Generate(context.Background(), VoiceoverInput{SceneID: scene.ID, Text: scene.Text["en"], Language: "en"})
	require.NoError(t, err)
	scene.Voiceover = map[Language]AudioReference{"en": vo}
	result := &GenerateResult{SourceLanguage: "en", Scenes: []Scene{scene}}
	const id = "run-overlay-render-barrier"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{ID: id, Request: req, Status: RunStatusRunning, CurrentStage: StageCompilingAudio}))
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: id, AttemptID: "attempt-1"})
	ctx := kernobs.WithRun(context.Background(), run)
	t.Cleanup(func() { run.Finish() })
	e := &executionRun{r: runner, ctx: ctx, runID: id, req: req, exec: ExecutionContext{JobID: id}, result: result}
	return e, repo, audio, overlay
}

// TestOverlayRenderLanguageBarrier_SubmitsBeforeMasterHandOff pins guarantees
// 1 and 2 end to end: with the gate on, the enqueuer is hit while BOTH the
// master audio render and the overlay renderer are held behind their releases,
// the fork then claims the certified language (no re-submission), and the run
// completes with the durable reference.
func TestOverlayRenderLanguageBarrier_SubmitsBeforeMasterHandOff(t *testing.T) {
	t.Setenv("OVERLAY_RENDER_LANG_BARRIER", "enabled")
	e, repo, audio, overlay := newOverlayBarrierFixture(t)

	finished := make(chan bool, 1)
	go func() { finished <- e.audioCompile() }()

	require.Eventually(t, func() bool { return overlay.calls.Load() >= 1 }, 3*time.Second, time.Millisecond,
		"the barrier must submit the overlay render while the render releases are still held")
	require.Zero(t, repo.checkpoints.Load())

	close(audio.release)
	awaitAudioBarrier(t, audio.drained)
	// The fork join cannot complete before the barrier's submission returns.
	select {
	case <-finished:
		t.Fatal("the run finalised before the overlay barrier joined")
	default:
	}
	close(overlay.release)

	require.True(t, <-finished)
	require.Equal(t, int32(1), overlay.calls.Load(), "exactly once: a certified language is never re-submitted")
	require.NotNil(t, e.result.OverlayRender, "the barrier-certified reference must reach the durable result")
	require.Equal(t, int32(1), repo.checkpoints.Load())
	require.NotNil(t, e.result.EditingTimeline)
}

// TestOverlayRenderLanguageBarrier_GateOffKeepsMasterHandOff pins the inert
// default: with the gate off the fork alone owns the single submission
func TestOverlayRenderLanguageBarrier_GateOffKeepsMasterHandOff(t *testing.T) {
	t.Setenv("OVERLAY_RENDER_LANG_BARRIER", "disabled")
	e, repo, audio, overlay := newOverlayBarrierFixture(t)

	finished := make(chan bool, 1)
	go func() { finished <- e.audioCompile() }()

	awaitAudioBarrier(t, audio.started)
	// The fork hands off at OnPlanReady, which precedes the master render, so
	// today's geometry already carries ONE submission while the releases are
	// held; the gate-off invariant is that the fork never adds a second one.
	time.Sleep(50 * time.Millisecond)
	require.LessOrEqual(t, overlay.calls.Load(), int32(1))
	require.Zero(t, repo.checkpoints.Load())

	close(audio.release)
	close(overlay.release)
	require.True(t, <-finished)
	require.Equal(t, int32(1), overlay.calls.Load(), "the fork renders exactly once with the gate off")
	require.NotNil(t, e.result.OverlayRender)
	require.Equal(t, int32(1), repo.checkpoints.Load())
}

// TestOverlayRenderLanguageBarrier_FailClosedRerender pins guarantee 3: the
// barrier's error is absorbed, the fork re-renders the unclaimed language, and
// the run fails on the same render-boundary surface as today.
func TestOverlayRenderLanguageBarrier_FailClosedRerender(t *testing.T) {
	t.Setenv("OVERLAY_RENDER_LANG_BARRIER", "enabled")
	e, repo, audio, overlay := newOverlayBarrierFixture(t)
	overlay.err = errors.New("primary overlay failure")

	finished := make(chan bool, 1)
	go func() { finished <- e.audioCompile() }()

	require.Eventually(t, func() bool { return overlay.calls.Load() >= 1 }, 3*time.Second, time.Millisecond,
		"the barrier submits its (doomed) attempt even with the master render released")
	close(audio.release)
	close(overlay.release)

	require.False(t, <-finished, "the re-rendered language failure must fail the run")
	require.Eventually(t, func() bool { return overlay.calls.Load() >= 2 }, time.Second, time.Millisecond,
		"the barrier attempt plus the fork's re-render must both have been submitted")
	require.Equal(t, int32(2), overlay.calls.Load(), "the barrier attempt plus the fork's re-render")
	require.Nil(t, e.result.OverlayRender, "a failed render must not leave a reference")
	require.Nil(t, e.result.EditingTimeline)
	require.Zero(t, repo.checkpoints.Load(), "failure must not checkpoint unfinished siblings")
	persisted, err := repo.Get(context.Background(), e.runID)
	require.NoError(t, err)
	require.Equal(t, RunStatusFailed, persisted.Status)
	require.True(t, strings.Contains(persisted.ErrorMessage, "primary overlay failure"), persisted.ErrorMessage)
}

// barrierOverlaySeed is the unit-level seed for the barrier: a branch whose
// delivered outcome already carries the detached projected input.
func barrierOverlaySeed(input *GenerateResult, err error) *overlayPlanBranch {
	branch := &overlayPlanBranch{done: make(chan overlayPlanBranchOutcome, 1)}
	branch.done <- overlayPlanBranchOutcome{input: input, err: err}
	return branch
}

func barrierOverlayLanguagePlans(source Language, targets ...Language) *GenerateResult {
	input := &GenerateResult{SourceLanguage: source}
	input.OverlayPlan = &capabilityoverlay.OverlayPlan{
		SchemaVersion: capabilityoverlay.SchemaVersionPlan, PlanID: "plan-" + string(source) + "-src", VideoID: "video-src", Language: string(source),
		Items: []capabilityoverlay.OverlayItem{{ID: "phrase-" + string(source)}},
	}
	input.LocalizedOverlayPlans = make(map[Language]*capabilityoverlay.OverlayPlan)
	for _, lang := range targets {
		input.LocalizedOverlayPlans[lang] = &capabilityoverlay.OverlayPlan{
			SchemaVersion: capabilityoverlay.SchemaVersionPlan, PlanID: "plan-" + string(lang), VideoID: "video-" + string(lang), Language: string(lang),
			Items: []capabilityoverlay.OverlayItem{{ID: "phrase-" + string(lang)}},
		}
	}
	return input
}

func barrierOverlayBarrierRequest() GenerateRequest {
	req := defaultTestRequest()
	req.SourceLanguage = "en"
	req.Languages = []Language{"en", "it", "fr"}
	req.Render.Enabled = true
	return req
}

// TestOverlayLanguageBarrier_ClaimsEveryLanguageAndMergesOntoSnapshot pins the
// unit claims: the barrier renders every pending plan (source + localized), the
// fork's snapshot is claimed by every certified language, and the returned
// outcomes are exactly what the fork must carry into its durable join.
func TestOverlayLanguageBarrier_ClaimsEveryLanguageAndMergesOntoSnapshot(t *testing.T) {
	t.Setenv("OVERLAY_RENDER_LANG_BARRIER", "enabled")
	enqueuer := &languageCapturingRenderEnqueuer{}
	runner := NewRunner(newInMemRunRepository(), newStubTextGenerator(nil), newStubTranslator(), &entityTimelineVoiceoverGenerator{})
	runner.SetLogger(zap.NewNop())
	runner.SetOverlayRenderEnqueuer(enqueuer)
	req := barrierOverlayBarrierRequest()

	branch := barrierOverlaySeed(barrierOverlayLanguagePlans("en", "it", "fr"), nil)
	barrier := runner.startOverlayRenderBarrier(context.Background(), "run-barrier-unit", req, ExecutionContext{JobID: "job-barrier-unit"}, 0, branch)
	require.NotNil(t, barrier, "the gate-on overlap config must spawn the barrier")

	snapshot := &GenerateResult{OverlayPlan: branch.outcome().input.OverlayPlan}
	require.Eventually(t, func() bool {
		select {
		case <-barrier.finished:
			return true
		default:
			return false
		}
	}, 3*time.Second, time.Millisecond)
	require.ElementsMatch(t, []string{"en", "it", "fr"}, enqueuer.languages,
		"every pending language renders exactly once at the barrier")

	outcomes := barrier.applyToSnapshot(snapshot)
	require.Len(t, outcomes, 3)
	require.NotNil(t, snapshot.OverlayRender, "the source language claim lands on the certified render")
	require.Contains(t, snapshot.LocalizedOverlayRenders, Language("it"))
	require.Contains(t, snapshot.LocalizedOverlayRenders, Language("fr"))

	// Idempotent application: a second merge changes nothing and submits nothing.
	require.Len(t, barrier.applyToSnapshot(snapshot), 3)
	require.Equal(t, 3, len(enqueuer.languages), "no re-submission on a second merge")
}

// TestOverlayLanguageBarrier_SkipsSubmissionOnBranchFailure: a failed branch
// submits nothing and claims nothing; the fork re-renders everything.
func TestOverlayLanguageBarrier_SkipsSubmissionOnBranchFailure(t *testing.T) {
	t.Setenv("OVERLAY_RENDER_LANG_BARRIER", "enabled")
	enqueuer := &languageCapturingRenderEnqueuer{}
	runner := NewRunner(newInMemRunRepository(), newStubTextGenerator(nil), newStubTranslator(), &entityTimelineVoiceoverGenerator{})
	runner.SetLogger(zap.NewNop())
	runner.SetOverlayRenderEnqueuer(enqueuer)
	req := barrierOverlayBarrierRequest()

	barrier := runner.startOverlayRenderBarrier(context.Background(), "run-barrier-fail", req, ExecutionContext{JobID: "job-barrier-fail"}, 0, barrierOverlaySeed(nil, errors.New("branch failure")))
	require.NotNil(t, barrier)

	snapshot := &GenerateResult{}
	require.Eventually(t, func() bool {
		select {
		case <-barrier.finished:
			return true
		default:
			return false
		}
	}, 3*time.Second, time.Millisecond)
	require.Empty(t, enqueuer.languages, "a failed branch plan must submit nothing")
	outcomes := barrier.applyToSnapshot(snapshot)
	require.Empty(t, outcomes, "nothing certified, nothing claimed")
	require.Nil(t, snapshot.OverlayRender)
	require.Empty(t, snapshot.LocalizedOverlayRenders)
}

// TestOverlayRenderBarrierInertOnSkipConditions pins the no-authority start
// contract: the barrier never spawns where the master fork's semantics must
// stay untouched (final jobs, missing enqueuer, render off, resumed-past).
func TestOverlayRenderBarrierInertOnSkipConditions(t *testing.T) {
	t.Setenv("OVERLAY_RENDER_LANG_BARRIER", "enabled")
	runner := NewRunner(newInMemRunRepository(), newStubTextGenerator(nil), newStubTranslator(), &entityTimelineVoiceoverGenerator{})
	runner.SetLogger(zap.NewNop())
	runner.SetOverlayRenderEnqueuer(newBarrierOverlayTestRenderer())
	req := barrierOverlayBarrierRequest()
	branch := barrierOverlaySeed(barrierOverlayLanguagePlans("en"), nil)
	exec := ExecutionContext{JobID: "job-barrier-inert"}

	require.Nil(t, runner.startOverlayRenderBarrier(context.Background(), "r1", req, exec, 0, nil), "no plan branch, no barrier")

	finalReq := req
	finalReq.FinalJob = true
	require.Nil(t, runner.startOverlayRenderBarrier(context.Background(), "r2", finalReq, exec, 0, branch), "final jobs keep the OnPlanReady scheduling")

	noRenderReq := req
	noRenderReq.Render.Enabled = false
	require.Nil(t, runner.startOverlayRenderBarrier(context.Background(), "r3", noRenderReq, exec, 0, branch), "no render request, no barrier")

	require.Nil(t, runner.startOverlayRenderBarrier(context.Background(), "r4", req, exec, StageIndex(StageCompilingAudio)+1, branch), "resumed past the audio stage, no barrier")

	t.Setenv("OVERLAY_RENDER_LANG_BARRIER", "disabled")
	gateOffRunner := NewRunner(newInMemRunRepository(), newStubTextGenerator(nil), newStubTranslator(), &entityTimelineVoiceoverGenerator{})
	gateOffRunner.SetLogger(zap.NewNop())
	gateOffRunner.SetOverlayRenderEnqueuer(newBarrierOverlayTestRenderer())
	require.Nil(t, gateOffRunner.startOverlayRenderBarrier(context.Background(), "r5", req, exec, 0, branch), "gate off, no barrier")
}

// TestOverlayLanguageBarrier_FinalJobEndToEndWithPublish: a final-job run with
// the gate on completes and certifies the render through the fork path (the
// barrier is inert there by design), publishing the master audio while the
// render join is still pending.
func TestOverlayLanguageBarrier_FinalJobEndToEndWithPublish(t *testing.T) {
	t.Setenv("OVERLAY_RENDER_LANG_BARRIER", "enabled")
	e, _, audio, overlay := newOverlayBarrierFixture(t)
	e.req.FinalJob = true
	audioPath := filepath.Join(t.TempDir(), "final-audio.m4a")
	require.NoError(t, os.WriteFile(audioPath, []byte("certified audio"), 0o600))
	audio.path = audioPath
	publisher := &observingFinalAudioPublisher{called: make(chan FinalAudioReference, 1)}
	e.r.SetFinalAudioPublisher(publisher)

	finished := make(chan bool, 1)
	go func() { finished <- e.audioCompile() }()
	awaitAudioBarrier(t, audio.started)
	close(audio.release)
	select {
	case <-overlay.started:
	case <-time.After(3 * time.Second):
		t.Fatal("the fork render was never submitted")
	}
	close(overlay.release)
	awaitAudioBarrier(t, overlay.drained)
	require.True(t, <-finished)
	require.Equal(t, int32(1), overlay.calls.Load(), "final jobs keep today's single fork submission")
	require.NotNil(t, e.result.OverlayRender)
	require.Equal(t, audioPath, e.result.FinalAudio.Path)
	require.Empty(t, e.result.FinalAudio.DriveFileID, "final-job audio is published through the HTTPS Objectstore handoff")
}
