package runner

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"go.uber.org/zap"
)

// finalJobHost is the minimal Host surface touched by Execution.finalJob. The
// embedded nil Host makes any accidental call to another phase panic, so this
// fixture proves the final-job handoff is the only thing the phase does.
type finalJobHost struct {
	Host
	submits     int
	submitRunID string
	submitReq   contract.GenerateRequest
	submitRes   *contract.GenerateResult
}

func (h *finalJobHost) MeasurePhase(ctx context.Context, _ kernobs.StageName, fn func(context.Context) bool) bool {
	return fn(ctx)
}

func (h *finalJobHost) SubmitFinalJob(_ context.Context, runID string, req contract.GenerateRequest, result *contract.GenerateResult) bool {
	h.submits++
	h.submitRunID = runID
	h.submitReq = req
	h.submitRes = result
	return true
}

func (h *finalJobHost) Logger() *zap.Logger { return zap.NewNop() }

// TestFinalJobWithoutStockAlwaysSubmitsRemotely pins the Producer/Master
// boundary: a final_job has no local final render, so the phase must call the
// Master even when a LocalFinalVideo artifact is already present (for example
// left by an older attempt). The local artifact must never short-circuit the
// handoff.
func TestFinalJobWithoutStockAlwaysSubmitsRemotely(t *testing.T) {
	host := &finalJobHost{}
	result := &contract.GenerateResult{
		LocalFinalVideo: &contract.LocalFinalVideoArtifact{Path: "/tmp/not-a-master.mp4"},
	}
	execution := &Execution{
		host:   host,
		ctx:    context.Background(),
		runID:  "run-final-job-remote",
		req:    contract.GenerateRequest{FinalJob: true},
		result: result,
	}

	if !execution.finalJob() {
		t.Fatal("finalJob() must report success when the Master accepts the handoff")
	}
	if host.submits != 1 {
		t.Fatalf("SubmitFinalJob calls = %d, want 1: a final_job must always hand off", host.submits)
	}
	if host.submitRunID != "run-final-job-remote" {
		t.Fatalf("SubmitFinalJob runID = %q, want %q", host.submitRunID, "run-final-job-remote")
	}
	if !host.submitReq.FinalJob {
		t.Fatal("SubmitFinalJob received a request without final_job=true")
	}
	if host.submitRes != result {
		t.Fatal("SubmitFinalJob received a different result than the run result")
	}
	if result.LocalFinalVideo == nil {
		t.Fatal("the fixture's stale local artifact must remain untouched")
	}
}

// TestNonFinalRunDoesNotSubmitFinalJob is the control: only final_job runs
// enter the Master handoff.
func TestNonFinalRunDoesNotSubmitFinalJob(t *testing.T) {
	host := &finalJobHost{}
	execution := &Execution{
		host:  host,
		ctx:   context.Background(),
		runID: "run-without-final-job",
	}

	if !execution.finalJob() {
		t.Fatal("finalJob() must be a no-op success for a non-final run")
	}
	if host.submits != 0 {
		t.Fatalf("SubmitFinalJob calls = %d, want 0 for a non-final run", host.submits)
	}
}
