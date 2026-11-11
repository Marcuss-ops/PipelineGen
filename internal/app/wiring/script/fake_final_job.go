package script

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/app/knobs"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// FakeFinalJobEnv switches the remote handoff into simulation mode. The
// switch is process-scoped so a request payload cannot disable production
// rendering.
const FakeFinalJobEnv = "VELOX_FAKE_FINAL_JOB"

// FakeFinalJobRequested reports whether the local-only handoff was requested.
func FakeFinalJobRequested() bool {
	switch strings.ToLower(strings.TrimSpace(knobs.String(FakeFinalJobEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// NewFakeFinalJobSubmitter returns a simulated final-job port. Its receipt
// claims success for the local pipeline only and deliberately carries no
// artifact identity.
func NewFakeFinalJobSubmitter() scriptgen.FinalJobSubmitter { return fakeFinalJobSubmitter{} }

type fakeFinalJobSubmitter struct{}

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
