package scriptgeneration

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"go.uber.org/zap"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// sleepingRenderEnqueuer stands in for the RenderingGen queue: the blocking
// render is what this phase measures, so the stub must actually take time.
type sleepingRenderEnqueuer struct {
	delay time.Duration
	calls int
}

func (e *sleepingRenderEnqueuer) EnqueueChrononPlan(ctx context.Context, plan capabilityoverlay.OverlayPlan) (RenderReference, error) {
	e.calls++
	if e.delay > 0 {
		select {
		case <-time.After(e.delay):
		case <-ctx.Done():
			return RenderReference{}, ctx.Err()
		}
	}
	return RenderReference{
		JobID:  plan.PlanID,
		Status: "COMPLETED",
		Artifact: &RenderArtifact{
			ID: "overlay-artifact", Kind: "overlay", SHA256: "overlay-sha",
			SizeBytes: 1024, DriveFileID: "drive-overlay", DriveLink: "https://drive.example/overlay",
		},
	}, nil
}

// TestOverlayRenderPhaseOwnsItsStageAndNotTheAudioStage pins the attribution
// contract the phase split exists for.
//
// Before the split the blocking render ran inside the audio compile phase, so
// audio_compile's wall time was dominated by a video render it does not own and
// the render never appeared on the critical path. The kernel attributes a nested
// stage to its enclosing stage, so "the render must be a sibling" is a
// structural requirement, not a naming preference — hence the assertion that the
// render's stage wall is real AND that nothing is recorded under audio_compile.
//
// The end-to-end join geometry is asserted by the vertical slice and the
// barrier tests below; this test pins the boundary's own attribution.
func TestOverlayRenderPhaseOwnsItsStageAndNotTheAudioStage(t *testing.T) {
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: "job-render", AttemptID: "attempt-1"})
	ctx := kernobs.WithRun(context.Background(), run)

	const renderDelay = 120 * time.Millisecond
	enqueuer := &sleepingRenderEnqueuer{delay: renderDelay}
	runner := &Runner{overlayRenderEnqueuer: enqueuer, log: zap.NewNop()}

	req := defaultTestRequest()
	req.Render.Enabled = true
	result := &GenerateResult{
		OverlayPlan: &capabilityoverlay.OverlayPlan{SchemaVersion: capabilityoverlay.SchemaVersionPlan, PlanID: "plan-1", VideoID: "video-1"},
	}

	// Exercised exactly as the pipeline does: audioCompile() runs this phase
	// under its own stage wrapper, which is what makes the render a SIBLING of
	// the audio stage rather than a nested part of it.
	require.True(t, runner.measurePhase(ctx, StageOverlayRender, func(c context.Context) bool {
		return runner.runOverlayRenderPhase(c, "run-1", req, ExecutionContext{}, 0, audioCompileState{}, result)
	}), "the render phase must succeed")
	run.Finish()

	report := run.Report()
	var renderStage *kernobs.StageReport
	for i := range report.Stages {
		st := &report.Stages[i]
		if st.Name == string(StageOverlayRender) {
			renderStage = st
		}
		require.NotEqual(t, string(audioCompileStage), st.Name,
			"the render phase must not record anything under the audio compile stage")
	}
	require.NotNil(t, renderStage, "overlay_render must be recorded as its own stage, got %+v", report.Stages)
	require.GreaterOrEqual(t, renderStage.DurationMs, renderDelay.Milliseconds(),
		"overlay_render must carry the real render wall time, not a zero-width marker")
	require.False(t, renderStage.StartedAt.IsZero(), "overlay_render must be anchored so it can join the critical path")
	require.False(t, renderStage.FinishedAt.IsZero(), "overlay_render must be anchored so it can join the critical path")

	require.NotNil(t, result.OverlayRender, "the certified render reference must be persisted on the result")
	require.Equal(t, 1, enqueuer.calls)
}

// TestOverlayRenderPhaseSkipsWithoutWork pins that a run which did not ask for a
// render (or has no frozen plan) does not enqueue anything and does not fail.
// The skip conditions are the same ones the in-phase block used to check.
func TestOverlayRenderPhaseSkipsWithoutWork(t *testing.T) {
	enqueuer := &sleepingRenderEnqueuer{}
	runner := &Runner{overlayRenderEnqueuer: enqueuer, log: zap.NewNop()}

	req := defaultTestRequest()
	req.Render.Enabled = true

	// No frozen overlay plan.
	require.True(t, runner.runOverlayRenderPhase(context.Background(), "run-1", req, ExecutionContext{}, 0, audioCompileState{}, &GenerateResult{}))

	// Frozen plan but the request did not enable the render.
	req.Render.Enabled = false
	result := &GenerateResult{OverlayPlan: &capabilityoverlay.OverlayPlan{PlanID: "plan-1"}}
	require.True(t, runner.runOverlayRenderPhase(context.Background(), "run-1", req, ExecutionContext{}, 0, audioCompileState{}, result))

	// Frozen plan but the run already resumed past the audio stage: the render
	// was part of that stage's work, so it must not be re-waited on.
	req.Render.Enabled = true
	require.True(t, runner.runOverlayRenderPhase(context.Background(), "run-1", req, ExecutionContext{},
		StageIndex(StageCompilingAudio)+1, audioCompileState{}, result))

	require.Zero(t, enqueuer.calls, "no skip path may submit a render")
}

// TestAudioStageNamesAreDistinct pins that the four boundaries the audio pipeline
// was split into keep distinct stage names. Two boundaries sharing a name would
// make the breakdown report the same stage twice and make the dominant-operation
// join ambiguous.
func TestAudioStageNamesAreDistinct(t *testing.T) {
	stages := []kernobs.StageName{StageOverlayRender, StageAudioFinalize, StageAudioPublish, kernobs.StageName(audioCompileStage)}
	seen := map[kernobs.StageName]bool{}
	for _, st := range stages {
		require.NotEmpty(t, string(st), "every audio boundary stage needs a name")
		require.False(t, seen[st], "stage %q is declared twice", st)
		seen[st] = true
	}
}

type barrierAudioRenderer struct {
	started, release, drained chan struct{}
	err                       error
	calls                     atomic.Int32
}

func (r *barrierAudioRenderer) Render(ctx context.Context, plan capabilityaudio.CompiledAudioPlan, assets capabilityaudio.ResolvedAudioAssets) (FinalAudioReference, AudioPipelineMetrics, error) {
	r.calls.Add(1)
	close(r.started)
	defer close(r.drained)
	select {
	case <-ctx.Done():
		return FinalAudioReference{}, AudioPipelineMetrics{}, ctx.Err()
	case <-r.release:
	}
	if r.err != nil {
		return FinalAudioReference{}, AudioPipelineMetrics{}, r.err
	}
	ref, metrics, err := (&stubCombinedAudioRenderer{}).Render(ctx, plan, assets)
	ref.DurationUS = plan.DurationUS
	return ref, metrics, err
}

type barrierOverlayRenderer struct {
	started, release, drained chan struct{}
	err                       error
	calls                     atomic.Int32
}

func (r *barrierOverlayRenderer) EnqueueChrononPlan(ctx context.Context, plan capabilityoverlay.OverlayPlan) (RenderReference, error) {
	r.calls.Add(1)
	// Mutate the private transport snapshot to prove no alias to the
	// caller's result survives the launch boundary.
	plan.Items[0].Text = "private worker mutation"
	close(r.started)
	defer close(r.drained)
	select {
	case <-ctx.Done():
		return RenderReference{}, ctx.Err()
	case <-r.release:
	}
	if r.err != nil {
		return RenderReference{}, r.err
	}
	return RenderReference{JobID: plan.PlanID, Status: "COMPLETED"}, nil
}

type audioJoinRepository struct {
	*inMemRunRepository
	checkpoints atomic.Int32
}

func (r *audioJoinRepository) SavePartialResult(ctx context.Context, id string, result *GenerateResult) error {
	r.checkpoints.Add(1)
	return r.inMemRunRepository.SavePartialResult(ctx, id, result)
}

func newAudioOverlapFixture(t *testing.T) (*executionRun, *audioJoinRepository, *barrierAudioRenderer, *barrierOverlayRenderer, *kernobs.Run) {
	t.Helper()
	repo := &audioJoinRepository{inMemRunRepository: newInMemRunRepository()}
	runner := NewRunner(repo, newStubTextGenerator(nil), newStubTranslator(), &entityTimelineVoiceoverGenerator{}, newStubDocumentPublisher(), canonicalTestDocumentRenderer{})
	runner.SetLogger(zap.NewNop())
	audio := &barrierAudioRenderer{started: make(chan struct{}), release: make(chan struct{}), drained: make(chan struct{})}
	overlay := &barrierOverlayRenderer{started: make(chan struct{}), release: make(chan struct{}), drained: make(chan struct{})}
	runner.SetCombinedAudioRenderer(audio)
	runner.SetOverlayRenderEnqueuer(overlay)
	runner.SetOverlayCanvas(GoldenOverlayCanvas)
	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeCombinedTimeline
	req.Languages = []Language{"en"}
	req.Render.Enabled = true
	req.Docs.Enabled = false
	scene := Scene{ID: "scene-0", Index: 0, Text: map[Language]string{"en": "Growth matters more than ever."}, Annotations: overlayScene1Annotations(), Audio: capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover}}
	vo, err := (&entityTimelineVoiceoverGenerator{}).Generate(context.Background(), VoiceoverInput{SceneID: scene.ID, Text: scene.Text["en"], Language: "en"})
	require.NoError(t, err)
	scene.Voiceover = map[Language]AudioReference{"en": vo}
	result := &GenerateResult{SourceLanguage: "en", Scenes: []Scene{scene}}
	const id = "run-audio-overlap"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{ID: id, Request: req, Status: RunStatusRunning, CurrentStage: StageCompilingAudio}))
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: id, AttemptID: "attempt-1"})
	ctx := kernobs.WithRun(context.Background(), run)
	return &executionRun{r: runner, ctx: ctx, runID: id, req: req, exec: ExecutionContext{JobID: id}, result: result}, repo, audio, overlay, run
}

func awaitAudioBarrier(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("audio/overlay branch did not reach barrier")
	}
}

func TestAudioOverlayOverlapJoinsBeforeCheckpointAndResumes(t *testing.T) {
	e, repo, audio, overlay, run := newAudioOverlapFixture(t)
	finished := make(chan bool, 1)
	go func() { finished <- e.audioCompile() }()
	awaitAudioBarrier(t, audio.started)
	awaitAudioBarrier(t, overlay.started) // encode is still held: overlap proven
	require.Zero(t, repo.checkpoints.Load())
	close(audio.release)
	awaitAudioBarrier(t, audio.drained)
	select {
	case <-finished:
		t.Fatal("finalized before the overlay sibling joined")
	default:
	}
	require.Zero(t, repo.checkpoints.Load())
	close(overlay.release)
	require.True(t, <-finished)
	awaitAudioBarrier(t, overlay.drained)
	require.Equal(t, int32(1), repo.checkpoints.Load())
	require.NotNil(t, e.result.EditingTimeline)
	require.NotNil(t, e.result.OverlayRender)
	require.NotEqual(t, "private worker mutation", e.result.OverlayPlan.Items[0].Text)
	// Certified references are reused: retries cannot duplicate either render.
	require.True(t, e.audioCompile())
	require.Equal(t, int32(1), audio.calls.Load())
	require.Equal(t, int32(1), overlay.calls.Load())
	run.Finish()
	for _, stage := range run.Report().Stages {
		if stage.Name == string(StageOverlayRender) {
			require.True(t, stage.Independent)
		}
	}
}

func TestAudioOverlayFailureAndCancellationDrainBothBranches(t *testing.T) {
	for _, failure := range []string{"audio", "overlay", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			e, repo, audio, overlay, _ := newAudioOverlapFixture(t)
			if failure == "audio" {
				audio.err = errors.New("primary audio failure")
			} else if failure == "overlay" {
				overlay.err = errors.New("primary overlay failure")
			}
			ctx, cancel := context.WithCancel(e.ctx)
			defer cancel()
			e.ctx = ctx
			finished := make(chan bool, 1)
			go func() { finished <- e.audioCompile() }()
			awaitAudioBarrier(t, audio.started)
			awaitAudioBarrier(t, overlay.started)
			switch failure {
			case "audio":
				close(audio.release)
			case "overlay":
				close(overlay.release)
			default:
				cancel()
			}
			select {
			case ok := <-finished:
				require.False(t, ok)
			case <-time.After(3 * time.Second):
				t.Fatal("failed siblings were not cancelled and joined")
			}
			awaitAudioBarrier(t, audio.drained)
			awaitAudioBarrier(t, overlay.drained)
			require.Zero(t, repo.checkpoints.Load(), "failure must not checkpoint unfinished siblings")
			require.Nil(t, e.result.EditingTimeline)
			require.Nil(t, e.result.OverlayRender)
			persisted, err := repo.Get(context.Background(), e.runID)
			require.NoError(t, err)
			require.Equal(t, RunStatusFailed, persisted.Status)
			require.Equal(t, 1, persisted.AttemptCount, "one attempt must record exactly one failure")
			if failure != "cancel" {
				require.True(t, strings.Contains(persisted.ErrorMessage, "primary "+failure+" failure"), persisted.ErrorMessage)
			}
		})
	}
}
