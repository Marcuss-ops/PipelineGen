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
// "the prewarm ran while the render was still waiting" is an observation and
// not a timing guess.
type blockingRenderEnqueuer struct {
	once    sync.Once
	started chan struct{}
	release chan struct{}
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

// prewarmSubmitter records the prewarm the runner starts. It implements both
// capabilities so the runner can type-assert the optional one.
type prewarmSubmitter struct {
	calls    int
	runID    string
	result   *GenerateResult
	finished chan struct{}
	once     sync.Once
}

func (s *prewarmSubmitter) SubmitFinalJob(context.Context, string, GenerateRequest, *GenerateResult) (RemoteFinalJobResult, error) {
	return RemoteFinalJobResult{}, nil
}

func (s *prewarmSubmitter) PrewarmFinalJobStockSources(_ context.Context, runID string, _ GenerateRequest, result *GenerateResult) error {
	s.calls++
	s.runID = runID
	s.result = result
	s.once.Do(func() { close(s.finished) })
	return nil
}

// TestFinalJobStockPrewarmRunsWhileTheOverlayRenderIsBlocked pins the schedule
// the prewarm exists for: r75 spent 9,8 s resolving stock sources inside
// `remote_final_job` while `overlay_render` occupied 13,4 s of the same critical
// path with an idle CPU. Starting the walk before the render means the two
// windows overlap; sequencing it after the render would add the cost instead of
// moving it.
func TestFinalJobStockPrewarmRunsWhileTheOverlayRenderIsBlocked(t *testing.T) {
	enqueuer := &blockingRenderEnqueuer{started: make(chan struct{}), release: make(chan struct{})}
	submitter := &prewarmSubmitter{finished: make(chan struct{})}
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
		phaseDone <- runner.runOverlayRenderPhase(context.Background(), "run-prewarm", req, ExecutionContext{}, 0, audioCompileState{}, result)
	}()

	select {
	case <-enqueuer.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the overlay render never started")
	}
	select {
	case <-submitter.finished:
	case <-time.After(5 * time.Second):
		t.Fatal("the stock prewarm did not run while the overlay render was blocked")
	}
	require.Equal(t, 1, submitter.calls)
	require.Equal(t, "run-prewarm", submitter.runID)
	require.NotNil(t, submitter.result, "the prewarm must receive the run result")
	require.Same(t, result.CanonicalTimeline, submitter.result.CanonicalTimeline,
		"the prewarm walks the same canonical timeline the handoff will use")

	close(enqueuer.release)
	select {
	case ok := <-phaseDone:
		require.True(t, ok, "the prewarm must never fail the render phase")
	case <-time.After(5 * time.Second):
		t.Fatal("the overlay render phase did not return")
	}
}

// TestFinalJobStockPrewarmSkippedWithoutAFinalJob keeps the warm-up off every
// run whose stock sources are never handed to the Master: a local render has no
// handoff to warm.
func TestFinalJobStockPrewarmSkippedWithoutAFinalJob(t *testing.T) {
	enqueuer := &blockingRenderEnqueuer{started: make(chan struct{}), release: make(chan struct{})}
	close(enqueuer.release)
	submitter := &prewarmSubmitter{finished: make(chan struct{})}
	runner := &Runner{overlayRenderEnqueuer: enqueuer, finalJobSubmitter: submitter, log: zap.NewNop()}

	req := defaultTestRequest()
	req.Render.Enabled = true
	result := &GenerateResult{OverlayPlan: &capmodel.OverlayPlan{PlanID: "plan-1"}}
	require.True(t, runner.runOverlayRenderPhase(context.Background(), "run-local", req, ExecutionContext{}, 0, audioCompileState{}, result))
	select {
	case <-submitter.finished:
		t.Fatal("a run without a final job must not warm the handoff cache")
	case <-time.After(50 * time.Millisecond):
	}
	require.Equal(t, 0, submitter.calls)
}

// TestFinalJobStockPrewarmStartsInTheAudioOverlapBranch pins WHERE the warm-up
// is anchored. This is the branch a real final job takes (r76 did): the overlay
// render runs on the sibling goroutine started by audioCompile and never enters
// runOverlayRenderPhase, so a hook placed only there is dead code in production
// — exactly what r76 measured when the handoff still resolved 56 sources
// serially while the render held the critical path.
func TestFinalJobStockPrewarmStartsInTheAudioOverlapBranch(t *testing.T) {
	e, _, audio, overlay, _ := newAudioOverlapFixture(t)
	submitter := &prewarmSubmitter{finished: make(chan struct{})}
	e.r.SetFinalJobSubmitter(submitter)
	e.req.FinalJob = true

	finished := make(chan bool, 1)
	go func() { finished <- e.audioCompile() }()
	select {
	case <-submitter.finished:
	case <-time.After(3 * time.Second):
		t.Fatal("the audio overlap branch did not start the final-job stock prewarm")
	}
	require.Equal(t, 1, submitter.calls)
	close(audio.release)
	close(overlay.release)
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("audioCompile did not return")
	}
}

// TestFinalJobStockPrewarmStartsOncePerRun pins the idempotence the two anchor
// points need: the join branch and the serial phase are exclusive today, but a
// resumed attempt re-entering a phase must not repeat the folder listings or
// the Drive work the first attempt already did.
func TestFinalJobStockPrewarmStartsOncePerRun(t *testing.T) {
	submitter := &prewarmSubmitter{finished: make(chan struct{})}
	runner := &Runner{finalJobSubmitter: submitter, log: zap.NewNop()}
	req := defaultTestRequest()
	req.FinalJob = true
	result := &GenerateResult{}
	runner.startFinalJobStockPrewarm(context.Background(), "run-once", req, result)
	<-submitter.finished
	runner.startFinalJobStockPrewarm(context.Background(), "run-once", req, result)
	require.Equal(t, 1, submitter.calls, "a second start for the same run must be a no-op")
	runner.startFinalJobStockPrewarm(context.Background(), "run-other", req, result)
	require.Eventually(t, func() bool { return submitter.calls == 2 }, time.Second, 5*time.Millisecond,
		"a different run must be warmed again")
}
