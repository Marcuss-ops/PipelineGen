// Package repositorytest owns the cross-backend conformance suite for the queue
// storage contract.
//
// Why a shared suite: memory and PostgreSQL are two implementations of ONE
// contract, and each used to be pinned only by its own tests. That is exactly
// how the idempotency index diverged (memory overwrote the key, PostgreSQL
// rejected it) while every backend-local test stayed green. A behavioural
// assertion that is not executed against both backends is a claim about one
// implementation, not about the contract.
//
// The suite never inspects backend internals: it talks to the JobRepository
// interface only, so it is also the executable definition of what a third
// backend would have to satisfy.
package repositorytest

import (
	"context"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository"
)

// Factory returns a fresh, empty repository for one subtest. PostgreSQL callers
// pass a helper that truncates the schema first.
type Factory func(t *testing.T) repository.JobRepository

// Run executes the conformance suite. Subtests are independent: each one asks
// the factory for its own repository, so a failure cannot leak rows into the
// next assertion.
func Run(t *testing.T, newRepository Factory) {
	t.Helper()
	ctx := context.Background()

	t.Run("claim on an empty queue returns no job", func(t *testing.T) {
		repo := newRepository(t)
		claimed, _, err := repo.Claim(ctx, "contract-worker")
		if err != nil {
			t.Fatalf("claim empty queue: %v", err)
		}
		if claimed != nil {
			t.Fatalf("claimed %q from an empty queue", claimed.ID)
		}
	})

	t.Run("stale generation cannot complete a job", func(t *testing.T) {
		repo := newRepository(t)
		job := model.Job{
			ID:         "contract-lifecycle",
			RenderPlan: []byte(`{"schema":"chronon.render-plan.v2","version":2}`),
		}
		if err := repo.Submit(ctx, job); err != nil {
			t.Fatalf("submit: %v", err)
		}
		stored, err := repo.Get(ctx, job.ID)
		if err != nil {
			t.Fatalf("get after submit: %v", err)
		}
		if stored.State != model.StatePending {
			t.Fatalf("state after submit = %q, want pending", stored.State)
		}
		claimed, lease, err := repo.Claim(ctx, "contract-worker")
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		if claimed == nil || claimed.ID != job.ID {
			t.Fatalf("claimed = %+v, want %q", claimed, job.ID)
		}
		if lease <= 0 || claimed.LeaseGeneration <= 0 {
			t.Fatalf("lease = %v generation = %d, want both positive", lease, claimed.LeaseGeneration)
		}
		if claimed.State != model.StateRunning {
			t.Fatalf("claimed state = %q, want running", claimed.State)
		}
		artifact := model.Artifact{ArtifactHash: "sha256:contract", SizeBytes: 42, Kind: "video"}
		// A stale attempt must match no row: the job is still owned by the
		// generation Claim handed out, and reporting success anyway is how a
		// zombie worker overwrites a re-render.
		if err := repo.Complete(ctx, claimed.ID, "contract-worker", claimed.LeaseGeneration+1, artifact); err == nil {
			t.Fatal("a stale lease generation completed the job")
		}
		if err := repo.Complete(ctx, claimed.ID, "another-worker", claimed.LeaseGeneration, artifact); err == nil {
			t.Fatal("a non-owner worker completed the job")
		}
		if still, err := repo.Get(ctx, claimed.ID); err != nil || still.State != model.StateRunning {
			t.Fatalf("rejected completion changed the job: state=%v err=%v", still.State, err)
		}
		if err := repo.Complete(ctx, claimed.ID, "contract-worker", claimed.LeaseGeneration, artifact); err != nil {
			t.Fatalf("complete with the current generation: %v", err)
		}
		done, err := repo.Get(ctx, claimed.ID)
		if err != nil {
			t.Fatalf("get after complete: %v", err)
		}
		if done.State != model.StateCompleted {
			t.Fatalf("state = %q, want completed", done.State)
		}
		if done.Artifact == nil {
			t.Fatal("completed job carries no artifact: the job would report success with nothing published")
		}
		if done.Artifact.ArtifactHash != artifact.ArtifactHash || done.Artifact.SizeBytes != artifact.SizeBytes {
			t.Fatalf("artifact = %+v, want hash %q size %d", done.Artifact, artifact.ArtifactHash, artifact.SizeBytes)
		}
		if next, _, err := repo.Claim(ctx, "contract-worker"); err != nil || next != nil {
			t.Fatalf("completed job was claimable again: job=%+v err=%v", next, err)
		}
	})

	t.Run("fail either requeues or permanently fails the job", func(t *testing.T) {
		repo := newRepository(t)
		job := model.Job{
			ID:         "contract-fail",
			RenderPlan: []byte(`{"schema":"chronon.render-plan.v2","version":2}`),
		}
		if err := repo.Submit(ctx, job); err != nil {
			t.Fatalf("submit: %v", err)
		}
		claimed, _, err := repo.Claim(ctx, "contract-worker")
		if err != nil || claimed == nil {
			t.Fatalf("claim: job=%+v err=%v", claimed, err)
		}
		if err := repo.Fail(ctx, claimed.ID, "contract-worker", claimed.LeaseGeneration, "contract failure"); err != nil {
			t.Fatalf("fail: %v", err)
		}
		after, err := repo.Get(ctx, claimed.ID)
		if err != nil {
			t.Fatalf("get after fail: %v", err)
		}
		// The attempt policy is configuration (max_attempts), so the suite pins
		// the invariant both policies must honour: a failed attempt either
		// returns the job to pending for a retry or reaches the terminal failed
		// state. It must never stay "running", which is the shape that leaves a
		// job invisible to every worker.
		switch after.State {
		case model.StatePending, model.StateFailed:
		default:
			t.Fatalf("state after fail = %q, want pending or failed", after.State)
		}
	})

	t.Run("cancel is terminal and unclaimable", func(t *testing.T) {
		repo := newRepository(t)
		job := model.Job{
			ID:         "contract-cancel",
			RenderPlan: []byte(`{"schema":"chronon.render-plan.v2","version":2}`),
		}
		if err := repo.Submit(ctx, job); err != nil {
			t.Fatalf("submit: %v", err)
		}
		if err := repo.Cancel(ctx, job.ID); err != nil {
			t.Fatalf("cancel: %v", err)
		}
		cancelled, err := repo.Get(ctx, job.ID)
		if err != nil {
			t.Fatalf("get after cancel: %v", err)
		}
		if cancelled.State != model.StateCancelled {
			t.Fatalf("state = %q, want cancelled", cancelled.State)
		}
		if claimed, _, err := repo.Claim(ctx, "contract-worker"); err != nil || claimed != nil {
			t.Fatalf("cancelled job was claimable: job=%+v err=%v", claimed, err)
		}
	})

	t.Run("children are returned in chunk order", func(t *testing.T) {
		repo := newRepository(t)
		parent := model.Job{
			ID:         "contract-parent",
			RenderPlan: []byte(`{"schema":"chronon.render-plan.v2","version":2}`),
		}
		if err := repo.Submit(ctx, parent); err != nil {
			t.Fatalf("submit parent: %v", err)
		}
		// Submitted out of order on purpose: the contract is chunk order, not
		// insertion order.
		for _, chunk := range []int{2, 0, 1} {
			child := model.Job{
				ID:          "contract-parent-chunk-" + string(rune('0'+chunk)),
				ParentJobID: parent.ID,
				ChunkIndex:  chunk,
				FrameRange:  &model.FrameRange{Start: int64(chunk * 120), End: int64((chunk + 1) * 120)},
				RenderPlan:  []byte(`{"schema":"chronon.render-plan.v2","version":2}`),
			}
			if err := repo.Submit(ctx, child); err != nil {
				t.Fatalf("submit child %d: %v", chunk, err)
			}
		}
		children, err := repo.Children(ctx, parent.ID)
		if err != nil {
			t.Fatalf("children: %v", err)
		}
		if len(children) != 3 {
			t.Fatalf("children = %d, want 3", len(children))
		}
		var order []string
		for i, child := range children {
			order = append(order, child.ID)
			if child.ChunkIndex != i {
				t.Fatalf("children[%d].ChunkIndex = %d, want %d (order %v)", i, child.ChunkIndex, i, strings.Join(order, ","))
			}
		}
	})
}
