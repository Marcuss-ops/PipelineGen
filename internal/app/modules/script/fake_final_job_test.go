package script

import (
	"context"
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

func TestFakeFinalJobRequested(t *testing.T) {
	for _, value := range []string{"", "0", "false", "no", "off", "  "} {
		t.Setenv(FakeFinalJobEnv, value)
		if FakeFinalJobRequested() {
			t.Fatalf("%s=%q enabled the simulated handoff", FakeFinalJobEnv, value)
		}
	}
	for _, value := range []string{"1", "true", "TRUE", "yes", "on", " 1 "} {
		t.Setenv(FakeFinalJobEnv, value)
		if !FakeFinalJobRequested() {
			t.Fatalf("%s=%q did not enable the simulated handoff", FakeFinalJobEnv, value)
		}
	}
}

func TestFakeFinalJobSubmitterCompletesWithoutArtifact(t *testing.T) {
	submitter := NewFakeFinalJobSubmitter()
	ctx := context.Background()

	submitted, err := submitter.SubmitFinalJob(ctx, "run-1", scriptgen.GenerateRequest{}, &scriptgen.GenerateResult{})
	if err != nil {
		t.Fatalf("SubmitFinalJob: %v", err)
	}
	if submitted.JobID == "" || submitted.Status != "SUCCEEDED" || !submitted.Simulated {
		t.Fatalf("receipt = %+v, want simulated success with an id", submitted)
	}
	if submitted.ArtifactURL != "" || submitted.SHA256 != "" {
		t.Fatalf("receipt = %+v, want no artifact claims", submitted)
	}
	starter, ok := submitter.(scriptgen.FinalJobStarter)
	if !ok {
		t.Fatal("fake submitter must implement FinalJobStarter")
	}
	started, err := starter.StartFinalJob(ctx, "run-1", scriptgen.GenerateRequest{}, &scriptgen.GenerateResult{})
	if err != nil || started.Status != "SUCCEEDED" {
		t.Fatalf("StartFinalJob = %+v, %v", started, err)
	}
	attacher, ok := submitter.(scriptgen.FinalJobAttacher)
	if !ok {
		t.Fatal("fake submitter must implement FinalJobAttacher")
	}
	attached, err := attacher.AttachFinalJob(ctx, submitted.JobID, false)
	if err != nil {
		t.Fatalf("AttachFinalJob: %v", err)
	}
	if attached.JobID != submitted.JobID || attached.Status != "SUCCEEDED" || !attached.Simulated {
		t.Fatalf("attached receipt = %+v, want same simulated identity", attached)
	}
	if _, err := attacher.AttachFinalJob(ctx, " ", false); err == nil {
		t.Fatal("AttachFinalJob accepted an empty job id")
	}
}
