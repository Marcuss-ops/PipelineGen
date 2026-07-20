package stockplan

import (
	"context"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// Coordinator is the canonical batch coordinator.
// It splits a temporal batch into per-group child stock jobs and
// persists durable batch/group/artifact state.
type Coordinator struct {
	repo      stockpipeline.StockBatchRepository
	enqueuer  JobEnqueuer
	resolver  PlanResolver
	stager    SourceStager
	log       *zap.Logger
	policyVer string
}

// CoordinatorDeps is the narrow constructor input for Coordinator.
type CoordinatorDeps struct {
	Repo     stockpipeline.StockBatchRepository
	Enqueuer JobEnqueuer
	Resolver PlanResolver
	Stager   SourceStager
	Log      *zap.Logger
}

// NewCoordinator constructs the canonical batch coordinator.
func NewCoordinator(deps CoordinatorDeps) *Coordinator {
	log := deps.Log
	if log == nil {
		log = zap.NewNop()
	}
	resolver := deps.Resolver
	if resolver == nil {
		resolver = NewDefaultResolver()
	}
	return &Coordinator{
		repo:      deps.Repo,
		enqueuer:  deps.Enqueuer,
		resolver:  resolver,
		stager:    deps.Stager,
		log:       log,
		policyVer: "v1",
	}
}

// Run creates a batch from spec, persists groups/artifacts and
// enqueues per-group child jobs.
func (c *Coordinator) Run(ctx context.Context, spec BatchSpec) (*BatchRunResult, error) {
	if c.repo == nil {
		return nil, fmt.Errorf("stockplan.coordinator: StockBatchRepository is required")
	}
	if c.enqueuer == nil {
		return nil, fmt.Errorf("stockplan.coordinator: JobEnqueuer is required")
	}

	plan, err := c.resolver.Resolve(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("stockplan.coordinator: resolve plan: %w", err)
	}

	// Ensure the policy is normalized before we read its fields.
	spec.Sampling.Normalize()

	// Drop groups that produced no clips (e.g. a custom resolver or an
	// out-of-range temporal slice). Only valid groups are persisted and
	// enqueued, so the batch counters stay consistent.
	validGroups := plan.Groups[:0]
	for _, g := range plan.Groups {
		if len(g.Clips) > 0 {
			validGroups = append(validGroups, g)
		}
	}
	if len(validGroups) == 0 {
		return nil, fmt.Errorf("stockplan.coordinator: no clips produced from any group")
	}
	plan.Groups = validGroups

	batchID := uuid.NewString()
	plan.BatchID = batchID

	totalClips := 0
	for _, g := range plan.Groups {
		totalClips += len(g.Clips)
	}

	batch := &stockpipeline.StockBatch{
		ID:             batchID,
		Fingerprint:    c.fingerprint(spec),
		SourceURL:      spec.SourceURL,
		SourceCacheKey: stockpipeline.DeriveSourceCacheKey(spec.SourceURL, "", "", false),
		RootFolderID:   spec.Destination.DriveFolderID,
		RootFolderName: spec.Destination.FolderName,
		Status:         stockpipeline.BatchStateRunning,
		ExpectedGroups: len(plan.Groups),
		ExpectedClips:  totalClips,
		PolicyVersion:  c.policyVer,
	}
	if err := c.repo.CreateBatch(ctx, batch); err != nil {
		return nil, fmt.Errorf("stockplan.coordinator: create batch: %w", err)
	}

	// Optional: warm the shared source cache after the batch is durable.
	if c.stager != nil {
		if staged, err := c.stager.StageSource(ctx, spec.SourceURL); err != nil {
			c.log.Warn("stockplan.coordinator: source staging failed, children will retry", zap.String("source_url", spec.SourceURL), zap.Error(err))
		} else if staged != nil {
			c.log.Info("stockplan.coordinator: shared source staged", zap.String("source_url", spec.SourceURL), zap.Int64("bytes", staged.Bytes))
		}
	}

	for i, g := range plan.Groups {
		if err := c.createGroupAndEnqueue(ctx, batchID, g, spec, i); err != nil {
			// Log the error but continue with other groups; the group
			// row will stay in PLANNED and a reconciler can retry.
			c.log.Error("stockplan.coordinator: failed to enqueue group",
				zap.String("batch_id", batchID),
				zap.String("group_key", g.Key),
				zap.Error(err))
		}
	}

	return &BatchRunResult{
		BatchID: batchID,
		Status:  string(stockpipeline.BatchStateRunning),
	}, nil
}

// Get returns the current batch status and its groups.
func (c *Coordinator) Get(ctx context.Context, batchID string) (*BatchStatus, error) {
	if c.repo == nil {
		return nil, fmt.Errorf("stockplan.coordinator: StockBatchRepository is required")
	}
	batch, err := c.repo.GetBatch(ctx, batchID)
	if err != nil {
		return nil, fmt.Errorf("stockplan.coordinator: get batch: %w", err)
	}
	if batch == nil {
		return nil, fmt.Errorf("stockplan.coordinator: batch %q not found", batchID)
	}
	groups, err := c.repo.ListGroups(ctx, batchID)
	if err != nil {
		return nil, fmt.Errorf("stockplan.coordinator: list groups: %w", err)
	}
	return &BatchStatus{Batch: batch, Groups: groups}, nil
}

func (c *Coordinator) createGroupAndEnqueue(ctx context.Context, batchID string, g PlannedGroup, spec BatchSpec, idx int) error {
	groupID := batchID + ":group:" + g.Key

	group := &stockpipeline.StockBatchGroup{
		ID:            groupID,
		BatchID:       batchID,
		GroupKey:      g.Key,
		Title:         g.Title,
		StartSec:      g.StartSec,
		EndSec:        g.EndSec,
		ExpectedClips: len(g.Clips),
		Status:        stockpipeline.GroupStatePlanned,
	}
	if err := c.repo.CreateGroup(ctx, group); err != nil {
		return fmt.Errorf("create group: %w", err)
	}

	for i, clip := range g.Clips {
		artifactID := groupID + ":clip:" + strconv.Itoa(i)
		art := &stockpipeline.StockArtifact{
			ID:                 artifactID,
			BatchID:            batchID,
			GroupID:            groupID,
			Ordinal:            i,
			ArtifactKey:        artifactKey(g, i),
			SourceURL:          spec.SourceURL,
			StartSec:           clip.StartSec,
			EndSec:             clip.EndSec,
			ExpectedDurationMs: spec.Sampling.ClipDurationSec * 1000,
			Status:             stockpipeline.ArtifactStatePlanned,
		}
		if err := c.repo.CreateArtifact(ctx, art); err != nil {
			c.log.Warn("stockplan.coordinator: create artifact failed",
				zap.String("artifact_id", artifactID),
				zap.Error(err))
		}
	}

	cmd := &stockpipeline.StockCommand{
		DirectURLs:    []string{spec.SourceURL},
		Clips:         g.Clips,
		FolderName:    spec.Destination.FolderName,
		DriveFolderID: spec.Destination.DriveFolderID,
		ClipDuration:  spec.Sampling.ClipDurationSec,
		Async:         true,
		Persist:       true,
	}

	jobID, err := c.enqueuer.Enqueue(ctx, &job.EnqueueRequest{
		Type:    appjobs.TypeMediaStock,
		Payload: cmd.ToJobPayload(),
	})
	if err != nil {
		return fmt.Errorf("enqueue child job: %w", err)
	}

	group.ChildJobID = jobID.ID
	group.Status = stockpipeline.GroupStateRunning
	if err := c.repo.UpdateGroupStatus(ctx, group.ID, group.Status, ""); err != nil {
		c.log.Warn("stockplan.coordinator: update group status failed",
			zap.String("group_id", group.ID),
			zap.Error(err))
	}

	c.log.Info("stockplan.coordinator: enqueued group child job",
		zap.String("batch_id", batchID),
		zap.String("group_key", g.Key),
		zap.String("child_job_id", jobID.ID),
		zap.Int("clips", len(g.Clips)))

	return nil
}

func (c *Coordinator) fingerprint(spec BatchSpec) string {
	return stockpipeline.DeriveSourceCacheKey(spec.SourceURL, "", "", false) +
		":" + strconv.Itoa(len(spec.Groups))
}

func artifactKey(g PlannedGroup, idx int) string {
	startMs := int64(g.Clips[idx].StartSec * 1000)
	endMs := int64(g.Clips[idx].EndSec * 1000)
	return fmt.Sprintf("clip_%03d_%09d_%09d", idx, startMs, endMs)
}
