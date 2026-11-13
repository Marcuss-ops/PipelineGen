package scriptgeneration

import (
	"context"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// blockingRenderEnqueuer holds the render open until the test releases it, so
// "the handoff started while the render was still waiting" is an observation
// and not a timing guess.
type blockingRenderEnqueuer struct {
	once    sync.Once
	started chan struct{}
	release chan struct{}
}

func (e *blockingRenderEnqueuer) EnqueueChrononPlanItems(ctx context.Context, plan capmodel.OverlayPlan) (RenderReference, error) {
	return e.EnqueueChrononPlan(ctx, plan)
}

func (e *blockingRenderEnqueuer) EnqueueChrononPlan(ctx context.Context, plan capmodel.OverlayPlan) (RenderReference, error) {
	e.once.Do(func() { close(e.started) })
	select {
	case <-e.release:
	case <-ctx.Done():
		return RenderReference{}, ctx.Err()
	}
	return RenderReference{JobID: plan.PlanID, Status: "COMPLETED"}, nil
}

// overlaySubmitter records whether the overlay path invokes the final-job
// handoff. The overlay phase must not start the handoff at all.
type overlaySubmitter struct {
	calls    int
	finished chan struct{}
	once     sync.Once
}

func (s *overlaySubmitter) SubmitFinalJob(context.Context, string, GenerateRequest, *GenerateResult) (RemoteFinalJobResult, error) {
	s.calls++
	s.once.Do(func() { close(s.finished) })
	return RemoteFinalJobResult{}, nil
}

// TestFinalJobDoesNotStartDuringOverlayRender pins the current contract: stock
// is passed by Drive reference, so local overlay rendering must not trigger
// source-resolution I/O or start the handoff.
func TestFinalJobDoesNotStartDuringOverlayRender(t *testing.T) {
	enqueuer := &blockingRenderEnqueuer{started: make(chan struct{}), release: make(chan struct{})}
	submitter := &overlaySubmitter{finished: make(chan struct{})}
	runner := &Runner{overlayRenderEnqueuer: enqueuer, finalJobSubmitter: submitter, log: zap.NewNop()}

	req := defaultTestRequest()
	req.FinalJob = true
	req.Render.Enabled = true
	result := &GenerateResult{
		OverlayPlan:       &capmodel.OverlayPlan{SchemaVersion: capmodel.SchemaVersionPlan, PlanID: "plan-1"},
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{DurationUS: 2_000_000},
	}

	phaseDone := make(chan bool, 1)
	go func() {
		phaseDone <- runner.runOverlayRenderPhase(context.Background(), "run-overlay", req, ExecutionContext{}, 0, audioCompileState{}, result)
	}()

	select {
	case <-enqueuer.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the overlay render never started")
	}
	select {
	case <-submitter.finished:
		t.Fatal("the overlay render must not start the final job")
	case <-time.After(50 * time.Millisecond):
	}
	require.Zero(t, submitter.calls)

	close(enqueuer.release)
	select {
	case ok := <-phaseDone:
		require.True(t, ok, "the overlay render phase must succeed")
	case <-time.After(5 * time.Second):
		t.Fatal("the overlay render phase did not return")
	}
}

// TestFinalJobDoesNotStartWithoutAFinalJob keeps the handoff off every run
// that has no remote final job.
func TestFinalJobDoesNotStartWithoutAFinalJob(t *testing.T) {
	enqueuer := &blockingRenderEnqueuer{started: make(chan struct{}), release: make(chan struct{})}
	close(enqueuer.release)
	submitter := &overlaySubmitter{finished: make(chan struct{})}
	runner := &Runner{overlayRenderEnqueuer: enqueuer, finalJobSubmitter: submitter, log: zap.NewNop()}

	req := defaultTestRequest()
	req.Render.Enabled = true
	result := &GenerateResult{OverlayPlan: &capmodel.OverlayPlan{PlanID: "plan-1"}}
	require.True(t, runner.runOverlayRenderPhase(context.Background(), "run-local", req, ExecutionContext{}, 0, audioCompileState{}, result))
	select {
	case <-submitter.finished:
		t.Fatal("a run without a final job must not start the handoff")
	case <-time.After(50 * time.Millisecond):
	}
	require.Equal(t, 0, submitter.calls)
}

// TestFinalJobDoesNotStartInTheAudioOverlapBranch keeps the no-I/O contract on
// the branch a real final job takes: the overlay render runs on the sibling
// goroutine started by audioCompile, which must not start the handoff.
func TestFinalJobDoesNotStartInTheAudioOverlapBranch(t *testing.T) {
	e, _, audio, overlay, _ := newAudioOverlapFixture(t)
	submitter := &overlaySubmitter{finished: make(chan struct{})}
	e.r.SetFinalJobSubmitter(submitter)
	e.req.FinalJob = true

	finished := make(chan bool, 1)
	go func() { finished <- e.audioCompile() }()
	select {
	case <-submitter.finished:
		t.Fatal("the audio overlap branch must not start the final job")
	case <-time.After(50 * time.Millisecond):
	}
	require.Zero(t, submitter.calls)
	close(audio.release)
	close(overlay.release)
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("audioCompile did not return")
	}
}
