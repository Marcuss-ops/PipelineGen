package clipresolver

import (
	"context"
	"fmt"

	jobs "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	domainartlist "github.com/Marcuss-ops/PipelineGen/internal/sources/artlist"
	"go.uber.org/zap"
)

// JobHarvestService implements ArtlistHarvestService using the jobs service
type JobHarvestService struct {
	jobsSvc       *jobs.Service
	log           *zap.Logger
	presetsConfig *domainartlist.PresetsConfig
	rootFolderID  string
}

// NewJobHarvestService creates a new JobHarvestService
func NewJobHarvestService(jobsSvc *jobs.Service, log *zap.Logger, presetsConfig *domainartlist.PresetsConfig, rootFolderID string) *JobHarvestService {
	return &JobHarvestService{
		jobsSvc:       jobsSvc,
		log:           log,
		presetsConfig: presetsConfig,
		rootFolderID:  rootFolderID,
	}
}

// EnqueueHarvest enqueues an artlist harvest job
func (s *JobHarvestService) EnqueueHarvest(ctx context.Context, term string, limit int, preset string) (string, error) {
	if s.jobsSvc == nil {
		return "", fmt.Errorf("jobs service is nil")
	}

	// Build RunTagRequest from preset
	req := &domainartlist.RunTagRequest{
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

	req.RootFolderID = s.rootFolderID
	if req.RootFolderID == "" {
		s.log.Warn("skipping artlist harvest enqueue because no root folder is configured",
			zap.String("term", term),
			zap.Int("limit", limit),
			zap.String("preset", preset))
		return "", nil
	}

	// Enqueue job
	job, err := s.jobsSvc.Enqueue(ctx, &jobs.EnqueueRequest{
		Type:       "artlist.run",
		Payload:    (&domainartlist.JobCodec{}).PayloadFromRequest(req),
		MaxRetries: 3,
		ActiveKey:  domainartlist.RunDedupKey(term, req.RootFolderID, req.Strategy, false),
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
