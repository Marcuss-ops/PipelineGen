package wiring

import (
	"context"
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// The switch is the operator's only way to measure the local pipeline without
// paying for a remote render, so both sides are pinned: it must be off by
// default and on for every accepted spelling.
func TestFakeFinalJobRequested(t *testing.T) {
	for _, value := range []string{"", "0", "false", "no", "off", "  "} {
		t.Setenv(fakeFinalJobEnv, value)
		if fakeFinalJobRequested() {
			t.Fatalf("%s=%q enabled the simulated handoff", fakeFinalJobEnv, value)
		}
	}
	for _, value := range []string{"1", "true", "TRUE", "yes", "on", " 1 "} {
		t.Setenv(fakeFinalJobEnv, value)
		if !fakeFinalJobRequested() {
			t.Fatalf("%s=%q did not enable the simulated handoff", fakeFinalJobEnv, value)
		}
	}
}

// A simulated receipt must be SUCCEEDED (so the run can finish) and must carry
// Simulated=true with no artifact: claiming an artifact would be a lie.
func TestFakeFinalJobSubmitterCompletesWithoutArtifact(t *testing.T) {
	submitter := newFakeFinalJobSubmitter()
	ctx := context.Background()

	submitted, err := submitter.SubmitFinalJob(ctx, "run-1", scriptgen.GenerateRequest{}, &scriptgen.GenerateResult{})
	if err != nil {
		t.Fatalf("SubmitFinalJob: %v", err)
	}
	if submitted.JobID == "" || submitted.Status != "SUCCEEDED" || !submitted.Simulated {
		t.Fatalf("receipt = %+v, want a simulated SUCCEEDED receipt with an id", submitted)
	}
	if submitted.ArtifactURL != "" || submitted.SHA256 != "" {
		t.Fatalf("receipt = %+v, want no artifact claims", submitted)
	}

	starter, ok := submitter.(scriptgen.FinalJobStarter)
	if !ok {
		t.Fatal("fake submitter must implement FinalJobStarter for the durable PREPARE path")
	}
	started, err := starter.StartFinalJob(ctx, "run-1", scriptgen.GenerateRequest{}, &scriptgen.GenerateResult{})
	if err != nil || started.Status != "SUCCEEDED" {
		t.Fatalf("StartFinalJob = %+v, %v", started, err)
	}

	attacher, ok := submitter.(scriptgen.FinalJobAttacher)
	if !ok {
		t.Fatal("fake submitter must implement FinalJobAttacher so a resumed run does not fail closed")
	}
	attached, err := attacher.AttachFinalJob(ctx, submitted.JobID, false)
	if err != nil {
		t.Fatalf("AttachFinalJob: %v", err)
	}
	if attached.JobID != submitted.JobID || attached.Status != "SUCCEEDED" || !attached.Simulated {
		t.Fatalf("attached receipt = %+v, want the same simulated identity", attached)
	}
	if _, err := attacher.AttachFinalJob(ctx, " ", false); err == nil {
		t.Fatal("AttachFinalJob accepted an empty job id")
	}
}
