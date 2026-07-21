package stockplan

import (
	"context"
	"fmt"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

// fakeBatchRepository is a minimal in-memory implementation of
// stockpipeline.StockBatchRepository for coordinator tests.
type fakeBatchRepository struct {
	batches   map[string]*stockpipeline.StockBatch
	groups    map[string]*stockpipeline.StockBatchGroup
	artifacts map[string]*stockpipeline.StockArtifact
}

func newFakeBatchRepository() *fakeBatchRepository {
	return &fakeBatchRepository{
		batches:   make(map[string]*stockpipeline.StockBatch),
		groups:    make(map[string]*stockpipeline.StockBatchGroup),
		artifacts: make(map[string]*stockpipeline.StockArtifact),
	}
}

func (r *fakeBatchRepository) CreateBatch(ctx context.Context, batch *stockpipeline.StockBatch) error {
	r.batches[batch.ID] = batch
	return nil
}

func (r *fakeBatchRepository) GetBatch(ctx context.Context, id string) (*stockpipeline.StockBatch, error) {
	return r.batches[id], nil
}

func (r *fakeBatchRepository) UpdateBatchStatus(ctx context.Context, id string, status stockpipeline.BatchState, lastError string) error {
	if b, ok := r.batches[id]; ok {
		b.Status = status
		b.LastError = lastError
	}
	return nil
}

func (r *fakeBatchRepository) CreateGroup(ctx context.Context, group *stockpipeline.StockBatchGroup) error {
	r.groups[group.ID] = group
	return nil
}

func (r *fakeBatchRepository) GetGroup(ctx context.Context, id string) (*stockpipeline.StockBatchGroup, error) {
	return r.groups[id], nil
}

func (r *fakeBatchRepository) UpdateGroupStatus(ctx context.Context, id string, status stockpipeline.GroupState, lastError string) error {
	if g, ok := r.groups[id]; ok {
		g.Status = status
		g.LastError = lastError
	}
	return nil
}

func (r *fakeBatchRepository) ListGroups(ctx context.Context, batchID string) ([]stockpipeline.StockBatchGroup, error) {
	var out []stockpipeline.StockBatchGroup
	for _, g := range r.groups {
		if g.BatchID == batchID {
			out = append(out, *g)
		}
	}
	return out, nil
}

func (r *fakeBatchRepository) CreateArtifact(ctx context.Context, artifact *stockpipeline.StockArtifact) error {
	r.artifacts[artifact.ID] = artifact
	return nil
}

func (r *fakeBatchRepository) GetArtifact(ctx context.Context, id string) (*stockpipeline.StockArtifact, error) {
	return r.artifacts[id], nil
}

func (r *fakeBatchRepository) MarkArtifactExtracting(ctx context.Context, id string) error {
	if a, ok := r.artifacts[id]; ok && (a.Status == stockpipeline.ArtifactStatePlanned || a.Status == stockpipeline.ArtifactStateRetryWait) {
		a.Status = stockpipeline.ArtifactStateExtracting
		a.Attempts++
		return nil
	}
	return fmt.Errorf("artifact %s not in extractable state", id)
}

func (r *fakeBatchRepository) MarkArtifactExtracted(ctx context.Context, id, localPath, sha256 string, actualDurationMs int) error {
	if a, ok := r.artifacts[id]; ok && a.Status == stockpipeline.ArtifactStateExtracting {
		a.Status = stockpipeline.ArtifactStateExtracted
		a.LocalPath = localPath
		a.SHA256 = sha256
		a.ActualDurationMs = actualDurationMs
		return nil
	}
	return fmt.Errorf("artifact %s not in EXTRACTING state", id)
}

func (r *fakeBatchRepository) MarkArtifactFailed(ctx context.Context, id string, status stockpipeline.ArtifactState, lastError string) error {
	if a, ok := r.artifacts[id]; ok && a.Status == stockpipeline.ArtifactStateExtracting {
		a.Status = status
		a.LastError = lastError
		return nil
	}
	return fmt.Errorf("artifact %s not in EXTRACTING state", id)
}

func (r *fakeBatchRepository) MarkArtifactPublished(ctx context.Context, id, driveFileID, driveFolderID, driveLink string) error {
	if a, ok := r.artifacts[id]; ok && a.Status == stockpipeline.ArtifactStateExtracted {
		a.Status = stockpipeline.ArtifactStatePublished
		a.DriveFileID = driveFileID
		a.DriveFolderID = driveFolderID
		a.DriveLink = driveLink
		return nil
	}
	return fmt.Errorf("artifact %s not in EXTRACTED state", id)
}

func (r *fakeBatchRepository) MarkArtifactVerified(ctx context.Context, id string) error {
	if a, ok := r.artifacts[id]; ok && a.Status == stockpipeline.ArtifactStatePublished {
		a.Status = stockpipeline.ArtifactStateVerified
		return nil
	}
	return fmt.Errorf("artifact %s not in PUBLISHED state", id)
}

func (r *fakeBatchRepository) MarkGroupSucceeded(ctx context.Context, id string, verifiedClips int) error {
	if g, ok := r.groups[id]; ok {
		g.Status = stockpipeline.GroupStateSucceeded
		g.VerifiedClips = verifiedClips
		return nil
	}
	return fmt.Errorf("group %s not found", id)
}

func (r *fakeBatchRepository) MarkBatchSucceeded(ctx context.Context, id string, verifiedClips int) error {
	if b, ok := r.batches[id]; ok {
		b.Status = stockpipeline.BatchStateSucceeded
		b.VerifiedClips = verifiedClips
		return nil
	}
	return fmt.Errorf("batch %s not found", id)
}

func (r *fakeBatchRepository) FindIncompleteArtifacts(ctx context.Context, groupID string, maxAttempts int) ([]stockpipeline.StockArtifact, error) {
	var out []stockpipeline.StockArtifact
	for _, a := range r.artifacts {
		if a.GroupID != groupID {
			continue
		}
		if a.Status == stockpipeline.ArtifactStateVerified || a.Status == stockpipeline.ArtifactStateFailedPermanent || a.Status == stockpipeline.ArtifactStateQuarantined {
			continue
		}
		if a.Attempts >= maxAttempts {
			continue
		}
		out = append(out, *a)
	}
	return out, nil
}

type fakeEnqueuer struct {
	jobs []*job.EnqueueRequest
	next int
}

func (e *fakeEnqueuer) Enqueue(ctx context.Context, req *job.EnqueueRequest) (*job.Job, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}
	e.jobs = append(e.jobs, req)
	e.next++
	return &job.Job{ID: fmt.Sprintf("job-%d", e.next)}, nil
}

type fakeStager struct {
	called bool
}

func (s *fakeStager) StageSource(ctx context.Context, url string) (*stockpipeline.StagedSource, error) {
	s.called = true
	return &stockpipeline.StagedSource{LocalPath: "/tmp/fake.mp4", Bytes: 1024}, nil
}

func TestCoordinator_Run_CreatesBatchAndEnqueuesGroups(t *testing.T) {
	repo := newFakeBatchRepository()
	queue := &fakeEnqueuer{}
	coordinator := NewCoordinator(CoordinatorDeps{
		Repo:     repo,
		Enqueuer: queue,
		Resolver: nil,
		Stager:   nil,
		Log:      zap.NewNop(),
	})

	spec := BatchSpec{
		SourceURL: "https://example.com/video.mp4",
		Destination: DestinationSpec{
			DriveFolderID: "drive-123",
			FolderName:    "Test",
		},
		Sampling: SamplingPolicy{ClipDurationSec: 4, MaxGroupDurationSec: 60, MaxClipsPerGroup: 15},
		Groups: []GroupSpec{
			{Key: "round-1", Title: "Round 1", StartSec: 0, EndSec: 60},
			{Key: "round-2", Title: "Round 2", StartSec: 60, EndSec: 120},
		},
	}

	result, err := coordinator.Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.BatchID == "" {
		t.Fatal("expected non-empty batch id")
	}
	if result.Status != string(stockpipeline.BatchStateRunning) {
		t.Fatalf("status = %q, want RUNNING", result.Status)
	}

	batch := repo.batches[result.BatchID]
	if batch == nil {
		t.Fatalf("batch %s not stored", result.BatchID)
	}
	if batch.ExpectedGroups != 2 {
		t.Fatalf("ExpectedGroups = %d, want 2", batch.ExpectedGroups)
	}
	if batch.ExpectedClips != len(queue.jobs)*15 {
		t.Logf("ExpectedClips=%d queued jobs=%d", batch.ExpectedClips, len(queue.jobs))
	}

	if len(queue.jobs) != 2 {
		t.Fatalf("expected 2 enqueued jobs, got %d", len(queue.jobs))
	}

	for _, g := range repo.groups {
		if g.Status != stockpipeline.GroupStateRunning {
			t.Fatalf("group %s status = %q, want RUNNING", g.GroupKey, g.Status)
		}
		if g.ChildJobID == "" {
			t.Fatalf("group %s has empty child job id", g.GroupKey)
		}
	}
}

func TestCoordinator_Get_ReturnsBatchAndGroups(t *testing.T) {
	repo := newFakeBatchRepository()
	coordinator := NewCoordinator(CoordinatorDeps{Repo: repo, Enqueuer: &fakeEnqueuer{}, Log: zap.NewNop()})

	spec := BatchSpec{
		SourceURL:   "https://example.com/video.mp4",
		Destination: DestinationSpec{DriveFolderID: "d", FolderName: "F"},
		Sampling:    SamplingPolicy{ClipDurationSec: 4, MaxGroupDurationSec: 60, MaxClipsPerGroup: 15},
		Groups:      []GroupSpec{{Key: "g", Title: "G", StartSec: 0, EndSec: 30}},
	}

	result, err := coordinator.Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	status, err := coordinator.Get(context.Background(), result.BatchID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if status.Batch == nil {
		t.Fatal("expected batch in status")
	}
	if status.Batch.ID != result.BatchID {
		t.Fatalf("batch id = %q, want %q", status.Batch.ID, result.BatchID)
	}
	if len(status.Groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(status.Groups))
	}
}

func TestCoordinator_Run_StagesSourceWhenStagerProvided(t *testing.T) {
	repo := newFakeBatchRepository()
	queue := &fakeEnqueuer{}
	stager := &fakeStager{}
	coordinator := NewCoordinator(CoordinatorDeps{
		Repo:     repo,
		Enqueuer: queue,
		Stager:   stager,
		Log:      zap.NewNop(),
	})

	spec := BatchSpec{
		SourceURL:   "https://example.com/video.mp4",
		Destination: DestinationSpec{DriveFolderID: "d", FolderName: "F"},
		Sampling:    SamplingPolicy{ClipDurationSec: 4, MaxGroupDurationSec: 60, MaxClipsPerGroup: 15},
		Groups:      []GroupSpec{{Key: "g", Title: "G", StartSec: 0, EndSec: 30}},
	}

	_, err := coordinator.Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !stager.called {
		t.Fatal("expected stager to be called")
	}
}

func TestCoordinator_Run_RequiresRepository(t *testing.T) {
	coordinator := NewCoordinator(CoordinatorDeps{Enqueuer: &fakeEnqueuer{}, Log: zap.NewNop()})
	_, err := coordinator.Run(context.Background(), BatchSpec{SourceURL: "https://x"})
	if err == nil {
		t.Fatal("expected error when repository is nil")
	}
}

func TestCoordinator_Run_RequiresEnqueuer(t *testing.T) {
	coordinator := NewCoordinator(CoordinatorDeps{Repo: newFakeBatchRepository(), Log: zap.NewNop()})
	_, err := coordinator.Run(context.Background(), BatchSpec{SourceURL: "https://x"})
	if err == nil {
		t.Fatal("expected error when enqueuer is nil")
	}
}
