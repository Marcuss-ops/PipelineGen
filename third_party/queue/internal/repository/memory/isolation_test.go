package memory

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/client"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// TestReturnedJobsAreDeepCopies pins the queue's state-isolation rule: a job
// handed to a caller (on claim or read) must not alias the map the repository
// guards with its mutex. ClaimState releases the write lock before it returns,
// so an escaped pointer would let a worker mutate state WITH NO LOCK — the
// exact concurrency hole the mutex exists to close. Every reference field is
// exercised, not just the struct: the render plan and assets arrive through
// Submit, and the artifact graph is attached on completion.
func TestReturnedJobsAreDeepCopies(t *testing.T) {
	ctx := context.Background()
	repo := New(time.Minute, 3)

	plan := json.RawMessage(`{"schema":"renderinggen.overlay-plan.v1","rev":1}`)
	assets := []model.AssetRef{{
		Hash:        "hash-original",
		LogicalPath: "assets/base.mp4",
		SourceURL:   "https://store/original",
	}}
	notBefore := time.Now().Add(-time.Minute)
	job := model.Job{
		ID:          "job-1",
		ParentJobID: "parent-1",
		ChunkIndex:  0,
		RenderPlan:  plan,
		Assets:      assets,
		FrameRange:  &model.FrameRange{Start: 0, End: 120},
		NotBefore:   &notBefore,
	}
	if err := repo.Submit(ctx, job); err != nil {
		t.Fatalf("submit: %v", err)
	}

	// The caller still owns the slices it submitted.
	plan[0] = 'X'
	assets[0].Hash = "hash-mutated"
	assets[0].SourceURL = "https://evil"

	stored, err := repo.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	assertOriginalJobPayload(t, stored, notBefore)

	claimed, _, err := repo.Claim(ctx, "worker-1")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed == nil || claimed.ID != job.ID {
		t.Fatalf("claim = %+v, want job-1", claimed)
	}

	// Mutate every caller-owned field of the claim result. None of it may
	// reach the stored job.
	claimed.ID = "hijacked"
	claimed.State = model.StateFailed
	claimed.RenderPlan[0] = 'Z'
	claimed.Assets[0].Hash = "claim-mutated"
	claimed.FrameRange.Start = 999
	*claimed.NotBefore = time.Now().Add(time.Hour)

	after, err := repo.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("get after claim mutation: %v", err)
	}
	if after.ID != job.ID || after.State != model.StateRunning || after.Worker != "worker-1" {
		t.Fatalf("claim mutation leaked into queue state: id=%q state=%q worker=%q", after.ID, after.State, after.Worker)
	}
	assertOriginalJobPayload(t, after, notBefore)

	artifact := model.Artifact{
		StorageKey:       "store/original.mp4",
		ChrononTelemetry: json.RawMessage(`{"gpu":"original"}`),
		Metrics:          map[string]float64{"frames": 120},
		OutputFacts: &model.OutputFacts{
			Container: "mp4",
			BatchSegments: []client.BatchSegmentFacts{{
				ItemID:      "item-1",
				OutputFacts: &model.OutputFacts{Container: "mp4"},
			}},
		},
		Provenance: &model.ArtifactProvenance{
			ItemIDs:           []string{"item-1"},
			ProducerTelemetry: json.RawMessage(`{"producer":"original"}`),
		},
	}
	if err := repo.Complete(ctx, job.ID, "worker-1", claimed.LeaseGeneration, artifact); err != nil {
		t.Fatalf("complete: %v", err)
	}

	// The artifact value the worker passed in is still owned by the worker.
	artifact.Metrics["frames"] = 999
	artifact.ChrononTelemetry[0] = 'X'
	artifact.OutputFacts.Container = "mutated"
	artifact.OutputFacts.BatchSegments[0].ItemID = "mutated"
	artifact.OutputFacts.BatchSegments[0].OutputFacts.Container = "mutated"
	artifact.Provenance.ItemIDs[0] = "mutated"
	artifact.Provenance.ProducerTelemetry[0] = 'X'

	done, err := repo.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("get completed: %v", err)
	}
	assertOriginalArtifact(t, done.Artifact)

	// A read result is caller-owned too: mutating it cannot rewrite the ledger.
	done.Artifact.Metrics["frames"] = 7
	done.Artifact.ChrononTelemetry[0] = 'Y'
	done.Artifact.OutputFacts.Container = "read-mutated"
	done.Artifact.OutputFacts.BatchSegments[0].ItemID = "read-mutated"
	done.Artifact.OutputFacts.BatchSegments[0].OutputFacts.Container = "read-mutated"
	done.Artifact.Provenance.ItemIDs[0] = "read-mutated"
	done.Artifact.Provenance.ProducerTelemetry[0] = 'Y'

	again, err := repo.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("get after read mutation: %v", err)
	}
	assertOriginalArtifact(t, again.Artifact)
}

func assertOriginalJobPayload(t *testing.T, job *model.Job, notBefore time.Time) {
	t.Helper()
	if got, want := string(job.RenderPlan), `{"schema":"renderinggen.overlay-plan.v1","rev":1}`; got != want {
		t.Fatalf("render plan = %s, want %s", got, want)
	}
	if len(job.Assets) != 1 || job.Assets[0].Hash != "hash-original" || job.Assets[0].SourceURL != "https://store/original" {
		t.Fatalf("assets = %+v, want the submitted originals", job.Assets)
	}
	if job.FrameRange == nil || job.FrameRange.Start != 0 || job.FrameRange.End != 120 {
		t.Fatalf("frame range = %+v, want [0,120)", job.FrameRange)
	}
	if job.NotBefore == nil || !job.NotBefore.Equal(notBefore) {
		t.Fatalf("not_before = %v, want %v", job.NotBefore, notBefore)
	}
}

func assertOriginalArtifact(t *testing.T, artifact *model.Artifact) {
	t.Helper()
	if artifact == nil {
		t.Fatal("artifact is nil")
	}
	if artifact.StorageKey != "store/original.mp4" {
		t.Fatalf("storage key = %q", artifact.StorageKey)
	}
	if got := artifact.Metrics["frames"]; got != 120 {
		t.Fatalf("metrics[frames] = %v, want 120", got)
	}
	if got := string(artifact.ChrononTelemetry); got != `{"gpu":"original"}` {
		t.Fatalf("chronon telemetry = %s", got)
	}
	if artifact.OutputFacts == nil || artifact.OutputFacts.Container != "mp4" {
		t.Fatalf("output facts = %+v", artifact.OutputFacts)
	}
	if len(artifact.OutputFacts.BatchSegments) != 1 || artifact.OutputFacts.BatchSegments[0].ItemID != "item-1" {
		t.Fatalf("batch segments = %+v", artifact.OutputFacts.BatchSegments)
	}
	segmentFacts := artifact.OutputFacts.BatchSegments[0].OutputFacts
	if segmentFacts == nil || segmentFacts.Container != "mp4" {
		t.Fatalf("segment output facts = %+v", segmentFacts)
	}
	if artifact.Provenance == nil || len(artifact.Provenance.ItemIDs) != 1 || artifact.Provenance.ItemIDs[0] != "item-1" {
		t.Fatalf("provenance = %+v", artifact.Provenance)
	}
	if got := string(artifact.Provenance.ProducerTelemetry); got != `{"producer":"original"}` {
		t.Fatalf("producer telemetry = %s", got)
	}
}

// TestConcurrentMutationOfClaimResultDoesNotRace runs the isolation contract
// under the race detector: one goroutine rewrites every mutable field of a
// claim result while another reads the job through the repository. If the
// claim returned the map's pointer, the writer and the reader would share the
// same backing arrays and -race would report it.
func TestConcurrentMutationOfClaimResultDoesNotRace(t *testing.T) {
	ctx := context.Background()
	repo := New(time.Minute, 3)
	if err := repo.Submit(ctx, model.Job{
		ID:         "job-1",
		RenderPlan: json.RawMessage(`{"n":1}`),
		Assets:     []model.AssetRef{{Hash: "h", LogicalPath: "p"}},
	}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	claimed, _, err := repo.Claim(ctx, "worker-1")
	if err != nil || claimed == nil {
		t.Fatalf("claim: job=%v err=%v", claimed, err)
	}
	id := claimed.ID

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			claimed.RenderPlan[0] = byte('a' + i%26)
			claimed.Assets[0].Hash = "mutated"
			claimed.State = model.StatePending
			claimed.Progress = &model.Progress{FramesDone: i}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			if _, err := repo.Get(ctx, id); err != nil {
				t.Errorf("get: %v", err)
				return
			}
			_ = repo.Stats(ctx)
		}
	}()
	wg.Wait()
}
