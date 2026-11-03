package wiring

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/app/knobs"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// fakeFinalJobEnv switches the remote final-job handoff into simulation mode.
//
// The switch exists to answer ONE question: how long does OUR pipeline take?
// With it on, the run still executes every local phase (script, semantic
// analysis, audio compile, overlay render, persistence, certification) and the
// final-job stage still runs, but no PREPARE is sent to the Master and no wait
// happens — the receipt is completed locally and marked Simulated. Everything
// after the handoff therefore measures the local pipeline, not the remote
// render.
//
// It is deliberately opt-in per process (never per payload): a production
// worker must not be able to silently skip renders because a client asked.
const fakeFinalJobEnv = "VELOX_FAKE_FINAL_JOB"

// fakeFinalJobRequested reports whether the simulated handoff was requested.
func fakeFinalJobRequested() bool {
	switch strings.ToLower(strings.TrimSpace(knobs.String(fakeFinalJobEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// fakeFinalJobSubmitter completes the handoff locally: no HTTP call, no lease,
// no artifact. It implements the full contract (submit/start/attach) so a run
// that resumes after a restart reattaches to the simulated receipt instead of
// failing on a missing attacher.
type fakeFinalJobSubmitter struct{}

func newFakeFinalJobSubmitter() scriptgen.FinalJobSubmitter { return fakeFinalJobSubmitter{} }

func fakeFinalJobReceipt(runID string) scriptgen.RemoteFinalJobResult {
	id := strings.TrimSpace(runID)
	if id == "" {
		id = "unknown-run"
	}
	return scriptgen.RemoteFinalJobResult{
		JobID:     "fake-final-job-" + id,
		Status:    "SUCCEEDED",
		Simulated: true,
	}
}

func (fakeFinalJobSubmitter) SubmitFinalJob(_ context.Context, runID string, _ scriptgen.GenerateRequest, _ *scriptgen.GenerateResult) (scriptgen.RemoteFinalJobResult, error) {
	return fakeFinalJobReceipt(runID), nil
}

func (fakeFinalJobSubmitter) StartFinalJob(_ context.Context, runID string, _ scriptgen.GenerateRequest, _ *scriptgen.GenerateResult) (scriptgen.RemoteFinalJobResult, error) {
	return fakeFinalJobReceipt(runID), nil
}

func (fakeFinalJobSubmitter) AttachFinalJob(_ context.Context, jobID string, _ bool) (scriptgen.RemoteFinalJobResult, error) {
	if strings.TrimSpace(jobID) == "" {
		return scriptgen.RemoteFinalJobResult{}, fmt.Errorf("fake final job attach requires a job id")
	}
	return scriptgen.RemoteFinalJobResult{JobID: jobID, Status: "SUCCEEDED", Simulated: true}, nil
}
