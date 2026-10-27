// Package remote_test — complete_job_test.go (P0 Commit 7, July 2026).
//
// Domain-level tests for the CompleteJob surface. These exercise the
// pure validation + idempotency-key derivation logic; the
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
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── CompleteJobIdempotencyKey (byte-stable SHA-256 triple) ───────────

func TestCompleteJobIdempotencyKey_DeterministicAcross1000Calls(t *testing.T) {
	const N = 1000
	key1 := remote.CompleteJobIdempotencyKey("j-1", 0, "h-1")
	for i := 0; i < N; i++ {
		k := remote.CompleteJobIdempotencyKey("j-1", 0, "h-1")
		if k != key1 {
			t.Fatalf("iteration %d: key drift (%s vs %s)", i, k, key1)
		}
	}
}

func TestCompleteJobIdempotencyKey_DifferentInputs_DifferentKeys(t *testing.T) {
	a := remote.CompleteJobIdempotencyKey("j-1", 0, "h-1")
	b := remote.CompleteJobIdempotencyKey("j-1", 1, "h-1") // different attempt
	if a == b {
		t.Errorf("attempt should distinguish idempotency keys: %s vs %s", a, b)
	}
	c := remote.CompleteJobIdempotencyKey("j-2", 0, "h-1") // different jobID
	if a == c {
		t.Errorf("jobID should distinguish idempotency keys: %s vs %s", a, c)
	}
	d := remote.CompleteJobIdempotencyKey("j-1", 0, "h-2") // different hash
	if a == d {
		t.Errorf("resultHash should distinguish idempotency keys: %s vs %s", a, d)
	}
}

func TestCompleteJobIdempotencyKey_EmptyInput_ReturnsEmptyMarker(t *testing.T) {
	// godlike/07 no-fake-availability: an empty input triple
	// MUST surface the empty-key marker (NOT a valid-looking
	// hash of "::" which would silently collide with realistic
	// "::" strings).
	tests := []struct {
		name    string
		jobID   string
		attempt int
		hash    string
	}{
		{"empty-jobID", "", 0, "h-1"},
		{"empty-hash", "j-1", 0, ""},
		{"negative-attempt", "j-1", -1, "h-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := remote.CompleteJobIdempotencyKey(tt.jobID, tt.attempt, tt.hash)
			if got != "" {
				t.Errorf("expected empty marker, got %q", got)
			}
		})
	}
}

func TestCompleteJobIdempotencyKey_ValidatesHex(t *testing.T) {
	key := remote.CompleteJobIdempotencyKey("j-1", 0, "h-1")
	if !remote.IsValidCompleteJobIdempotencyKey(key) {
		t.Errorf("expected valid hex, got %q", key)
	}
	// 63-char "almost hex" must fail.
	if remote.IsValidCompleteJobIdempotencyKey(key[:63]) {
		t.Errorf("63-char hex should be invalid")
	}
	// non-hex char must fail.
	if remote.IsValidCompleteJobIdempotencyKey(strings.Repeat("g", 64)) {
		t.Errorf("non-hex 'g' chars should be invalid")
	}
	// Empty marker is valid (callers MUST probe both surfaces).
	if !remote.IsValidCompleteJobIdempotencyKey("") {
		t.Errorf("empty marker must validate as valid (callers probe empty separately)")
	}
}

func TestCompleteJobIdempotencyKeyDiagnostic_EmptyInputs(t *testing.T) {
	if got := remote.CompleteJobIdempotencyKeyDiagnostic("", 0, "h"); !strings.Contains(got, "jobID") {
		t.Errorf("expected jobID in diagnostic: %s", got)
	}
	if got := remote.CompleteJobIdempotencyKeyDiagnostic("j-1", -1, "h"); !strings.Contains(got, "attempt") {
		t.Errorf("expected attempt in diagnostic: %s", got)
	}
	if got := remote.CompleteJobIdempotencyKeyDiagnostic("j-1", 0, ""); !strings.Contains(got, "resultHash") {
		t.Errorf("expected resultHash in diagnostic: %s", got)
	}
	// All-present triple returns empty diagnostic (caller knows ALL fields are set).
	if got := remote.CompleteJobIdempotencyKeyDiagnostic("j-1", 0, "h"); got != "" {
		t.Errorf("expected empty diagnostic for complete triple, got %s", got)
	}
}

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
