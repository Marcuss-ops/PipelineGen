// Package remote_test — complete_job_test.go (P0 Commit 7, July 2026).
//
// Domain-level tests for the CompleteJob response surface. The
// application-level single-TX orchestration is exercised in
// internal/capabilities/jobs/policy/complete_job_service_test.go.
//
// godlike/06 one canonical owner per fact: the typed envelopes
// here are the SSOT wire-shape contract; tests probe the validator
// invariants exhaustively so future schema-bumps must update the
// test surface (forward-prevention via type system + test pin).
package remote_test

import (
	"encoding/json"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── CompleteJobResponse round-trip ───────────────────────────────────

func TestCompleteJobResponse_JSONRoundTrip(t *testing.T) {
	resp := &remote.CompleteJobResponse{
		Status:         job.StatusSucceeded,
		JobArtifactIDs: []string{"j-1:voiceover", "j-1:metadata"},
		JobID:          "j-1",
		Attempt:        0,
		ResultHash:     "h-1",
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back remote.CompleteJobResponse
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Status != resp.Status {
		t.Errorf("status drift: %s vs %s", back.Status, resp.Status)
	}
	if len(back.JobArtifactIDs) != len(resp.JobArtifactIDs) {
		t.Errorf("artifact-ids drift: %d vs %d", len(back.JobArtifactIDs), len(resp.JobArtifactIDs))
	}
	for i := range resp.JobArtifactIDs {
		if back.JobArtifactIDs[i] != resp.JobArtifactIDs[i] {
			t.Errorf("artifact-id[%d] drift: %s vs %s", i, back.JobArtifactIDs[i], resp.JobArtifactIDs[i])
		}
	}
	if back.JobID != resp.JobID || back.Attempt != resp.Attempt || back.ResultHash != resp.ResultHash {
		t.Errorf("envelope echo fields drift: %+v vs %+v", back, resp)
	}
}
