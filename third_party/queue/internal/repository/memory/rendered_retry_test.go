package memory

import (
	"context"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// The staged worker claims ANY claimable state (pending + rendered): a
// rendered job must be re-claimable carrying its durable artifact so the
// worker retries only the external publication and never re-renders.
func TestRenderedJobIsReclaimableWithArtifact(t *testing.T) {
	repo := New(30*time.Second, 3)

	job := model.Job{ID: "job-rendered", RenderPlan: []byte(`{"n":1}`)}
	if err := repo.Submit(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	claimed, _, err := repo.ClaimState(context.Background(), "w1", "")
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	artifact := model.Artifact{
		StorageKey:   "sha-rendered",
		ArtifactHash: "sha-rendered",
		SizeBytes:    42,
		ContentType:  "video/mp4",
	}
	if err := repo.Rendered(context.Background(), "job-rendered", "w1", artifact, "drive upload failed"); err != nil {
		t.Fatal(err)
	}

	// A pending-only claim (the old worker behavior) must NOT pick it up.
	pending := model.Job{ID: "job-pending", RenderPlan: []byte(`{"n":2}`)}
	if err := repo.Submit(context.Background(), pending); err != nil {
		t.Fatal(err)
	}
	got, _, err := repo.ClaimState(context.Background(), "w2", model.StatePending)
	if err != nil || got == nil {
		t.Fatalf("pending claim: %v %v", got, err)
	}
	if got.ID != "job-pending" {
		t.Fatalf("pending claim returned %q, want job-pending", got.ID)
	}

	// A blank-state claim (any claimable state) returns the rendered job with
	// its artifact attached.
	got, _, err = repo.ClaimState(context.Background(), "w3", "")
	if err != nil || got == nil {
		t.Fatalf("blank claim: %v %v", got, err)
	}
	if got.ID != "job-rendered" {
		t.Fatalf("blank claim returned %q, want job-rendered", got.ID)
	}
	if got.State != model.StateRunning || got.Worker != "w3" {
		t.Fatalf("reclaimed job state: %+v", got)
	}
	if got.Artifact == nil || got.Artifact.StorageKey != "sha-rendered" {
		t.Fatalf("reclaimed job must carry its durable artifact: %+v", got.Artifact)
	}
	if got.Attempts != 2 {
		t.Fatalf("reclaim must count a new attempt, got %d", got.Attempts)
	}

	// And it can be completed by the new owner (publication-only retry done).
	if err := repo.Complete(context.Background(), "job-rendered", "w3", artifact); err != nil {
		t.Fatalf("complete after publication retry: %v", err)
	}
}

func TestRenderedStateIsNeverLeaseRequeued(t *testing.T) {
	repo := New(30*time.Second, 3)

	job := model.Job{ID: "job-rendered", RenderPlan: []byte(`{"n":1}`)}
	if err := repo.Submit(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	claimed, _, err := repo.ClaimState(context.Background(), "w1", "")
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	if err := repo.Rendered(context.Background(), "job-rendered", "w1", model.Artifact{
		StorageKey: "sha", ArtifactHash: "sha", SizeBytes: 1, ContentType: "video/mp4",
	}, "drive down"); err != nil {
		t.Fatal(err)
	}

	// Expired-lease requeue must not touch rendered jobs (they hold no lease
	// and are claimable on demand): a far-future clock changes nothing.
	n, err := repo.RequeueExpired(context.Background(), time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("requeue expired touched %d jobs, want 0", n)
	}
	got, err := repo.Get(context.Background(), "job-rendered")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != model.StateRendered {
		t.Fatalf("rendered job state changed to %q", got.State)
	}
}
