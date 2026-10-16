package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/client"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository/memory"
)

// The audit's distributed test matrix scenario 1: a duplicate submit whose
// payload DIFFERS from the original under the same identity. The queue
// answers 409 on job identity alone, and producers treat that 409 as a
// replay-safe signal. That treatment is only correct if the canonical job —
// the first submitter's work — is preserved untouched and the divergent
// payload is rejected, never merged.

func replayJob(id, idemKey string, planPayload string) model.Job {
	return model.Job{
		ID:             id,
		IdempotencyKey: idemKey,
		RenderPlan:     json.RawMessage(planPayload),
		State:          model.StatePending,
	}
}

// TestDuplicateSubmitSameIDDifferentPayload pins the replay contract on the
// same-ID path: the FIRST payload wins, the second is rejected, and the
// canonical job's stored plan is byte-identical to the first submitter's.
func TestDuplicateSubmitSameIDDifferentPayload(t *testing.T) {
	svc := New(memory.New(30*time.Second, 3))
	ctx := context.Background()

	planA := []byte(`{"schema":"renderinggen.overlay-plan.v1","job_id":"j1","plan_rev":1}`)
	planB := []byte(`{"schema":"renderinggen.overlay-plan.v1","job_id":"j1","plan_rev":2}`)

	if err := svc.Submit(ctx, replayJob("j1", "", string(planA))); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	err := svc.Submit(ctx, replayJob("j1", "", string(planB)))
	if !errors.Is(err, client.ErrJobExists) {
		t.Fatalf("duplicate submit with different payload: want ErrJobExists, got %v", err)
	}

	canonical, getErr := svc.Get(ctx, "j1")
	if getErr != nil {
		t.Fatalf("get canonical: %v", getErr)
	}
	if string(canonical.RenderPlan) != string(planA) {
		t.Fatalf("canonical plan was overwritten by a divergent replay: got %s, want %s", canonical.RenderPlan, planA)
	}
	if canonical.State != model.StatePending {
		t.Fatalf("canonical state = %q, want pending", canonical.State)
	}
}

// TestDuplicateSubmitSameIdempotencyKeyDifferentID pins the idempotency-key
// path: a replay under the same logical key returns the canonical job
// (created=false) even when the replaying producer minted a different ID.
func TestDuplicateSubmitSameIdempotencyKeyDifferentID(t *testing.T) {
	svc := New(memory.New(30*time.Second, 3))
	ctx := context.Background()

	planA := []byte(`{"schema":"renderinggen.overlay-plan.v1","job_id":"j-a","plan_rev":1}`)

	if err := svc.Submit(ctx, replayJob("j-a", "run-1/chunk-0", string(planA))); err != nil {
		t.Fatalf("first submit: %v", err)
	}

	canonical, created, err := svc.SubmitIdempotent(ctx, replayJob("j-b", "run-1/chunk-0", string(planA)))
	if err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
	if created {
		t.Fatal("idempotent replay minted a second job for the same key")
	}
	if canonical == nil || canonical.ID != "j-a" {
		t.Fatalf("canonical job = %+v, want job j-a", canonical)
	}

	// But a genuinely different logical request under the same key must NOT
	// be silently mapped onto the first one: identity divergence is a
	// producer-side bug and surfaces as the canonical mismatch, so the test
	// pins which job the replay resolves to.
	if string(canonical.RenderPlan) != string(planA) {
		t.Fatalf("canonical plan = %s, want %s", canonical.RenderPlan, planA)
	}
}
