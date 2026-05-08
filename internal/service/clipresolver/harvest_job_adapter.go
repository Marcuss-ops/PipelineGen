package clipresolver

import (
	"context"
	"fmt"

	jobservice "velox/go-master/internal/service/artlist"
	"velox/go-master/internal/service/jobs"
	"velox/go-master/pkg/models"
	"go.uber.org/zap"
)

// JobHarvestService implements ArtlistHarvestService using the jobs service
type JobHarvestService struct {
	jobsSvc      *jobs.Service
	log          *zap.Logger
	presetsConfig *jobservice.PresetsConfig
}

// NewJobHarvestService creates a new JobHarvestService
func NewJobHarvestService(jobsSvc *jobs.Service, log *zap.Logger, presetsConfig *jobservice.PresetsConfig) *JobHarvestService {
	return &JobHarvestService{
		jobsSvc:      jobsSvc,
		log:          log,
		presetsConfig: presetsConfig,
	}
}

// EnqueueHarvest enqueues an artlist harvest job
func (s *JobHarvestService) EnqueueHarvest(ctx context.Context, term string, limit int, preset string) (string, error) {
	if s.jobsSvc == nil {
		return "", fmt.Errorf("jobs service is nil")
	}

	// Build RunTagRequest from preset
	req := &jobservice.RunTagRequest{
		Term:     term,
		Limit:    limit,
		Strategy: "verify",
	}

	// Apply preset if specified
	if preset != "" && s.presetsConfig != nil {
		if p, ok := s.presetsConfig.Presets[preset]; ok {
			req.Strategy = p.Strategy
			req.ClipDuration = p.ClipDuration
			req.Width = p.Width
			req.Height = p.Height
			req.FPS = p.FPS
		}
	}

	// Enqueue job
	job, err := s.jobsSvc.Enqueue(ctx, &jobs.EnqueueRequest{
		Type:       models.JobTypeArtlistRun,
		Payload:    req.ToMap(),
		MaxRetries: 3,
		ActiveKey:  jobservice.RunDedupKey(term, req.RootFolderID, req.Strategy, false),
	})
	if err != nil {
		return "", fmt.Errorf("failed to enqueue harvest job: %w", err)
	}

	s.log.Info("enqueued artlist harvest job",
		zap.String("job_id", job.ID),
		zap.String("term", term),
		zap.Int("limit", limit),
		zap.String("preset", preset))

	return job.ID, nil
}
