package stockpipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	pkgretry "github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

type stagedFailureStep struct {
	err error
}

func (s stagedFailureStep) Name() string { return "stock.test_terminal_cleanup" }

func (s stagedFailureStep) Run(_ context.Context, runner StepRunner) error {
	runner.State().StagedAssets = []*assets.StagedAsset{{
		LocalPath: "/tmp/stock-terminal-cleanup/source.mp4",
		SourceID:  "https://example.com/source.mp4",
		Bytes:     42,
	}}
	return s.err
}

func runStagedFailureCleanupTest(t *testing.T, runErr error) *recordingStager {
	t.Helper()
	rec := &recordingStager{}
	o := newWiringTestOrchestrator(rec)
	o.dispatchSteps = []Step{stagedFailureStep{err: runErr}}

	_, err := o.RunResilient(context.Background(), &RunInput{})
	if !errors.Is(err, runErr) {
		t.Fatalf("RunResilient error = %v, want errors.Is(..., %v)", err, runErr)
	}
	return rec
}

func TestRunResilient_TerminalFailureCleansStagedSources(t *testing.T) {
	rec := runStagedFailureCleanupTest(t, errors.New("ffmpeg cut: validation: invalid input"))
	if rec.cleanupCalls != 1 {
		t.Fatalf("terminal failure cleanup calls = %d, want 1", rec.cleanupCalls)
	}
}

func TestRunResilient_TransientFailureRetainsStagedSources(t *testing.T) {
	runErr := &pkgretry.TransientInfrastructureError{Err: errors.New("yt-dlp: transient network timeout")}
	rec := runStagedFailureCleanupTest(t, runErr)
	if rec.cleanupCalls != 0 {
		t.Fatalf("transient failure cleanup calls = %d, want 0 so retry can resume staged sources", rec.cleanupCalls)
	}
}

func TestRunResilient_CancellationCleansStagedSources(t *testing.T) {
	rec := runStagedFailureCleanupTest(t, context.Canceled)
	if rec.cleanupCalls != 1 {
		t.Fatalf("cancellation cleanup calls = %d, want 1", rec.cleanupCalls)
	}
}
