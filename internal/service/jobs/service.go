package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/repository/jobs"
	"velox/go-master/pkg/models"
)

type Service struct {
	repo       *jobs.Repository
	dispatcher *Dispatcher
	log        *zap.Logger
	leaseTTL   time.Duration
}

func NewService(repo *jobs.Repository, dispatcher *Dispatcher, log *zap.Logger) *Service {
	return &Service{
		repo:       repo,
		dispatcher: dispatcher,
		log:        log,
		leaseTTL:   5 * time.Minute,
	}
}

func (s *Service) SetLeaseTTL(ttl time.Duration) {
	s.leaseTTL = ttl
}

func (s *Service) Enqueue(ctx context.Context, req *EnqueueRequest) (*models.Job, error) {
	if req.ActiveKey != "" {
		existing, err := s.repo.FindActiveByKey(ctx, req.ActiveKey)
		if err != nil {
			return nil, fmt.Errorf("failed to check existing job: %w", err)
		}
		if existing != nil && !existing.Status.IsTerminal() {
			s.log.Info("returning existing job with same active key", zap.String("job_id", existing.ID))
			return existing, nil
		}
	}

	now := time.Now()
	job := &models.Job{
		ID:          generateJobID(),
		Type:        req.Type,
		Status:      models.StatusQueued,
		Priority:    req.Priority,
		Project:     req.Project,
		VideoName:   req.VideoName,
		Payload:     req.Payload,
		RetryCount:  0,
		MaxRetries:  req.MaxRetries,
		Progress:    0,
		CreatedAt:   now,
		UpdatedAt:   now,
		ActiveKey:   req.ActiveKey,
	}

	if job.MaxRetries == 0 {
		job.MaxRetries = 3
	}

	if job.Payload == nil {
		job.Payload = make(map[string]interface{})
	}

	if err := s.repo.Create(ctx, job); err != nil {
		return nil, fmt.Errorf("failed to create job: %w", err)
	}

	s.log.Info("job enqueued", zap.String("job_id", job.ID), zap.String("type", string(job.Type)))
	return job, nil
}

func (s *Service) Get(ctx context.Context, id string) (*models.Job, error) {
	return s.repo.Get(ctx, id)
}

func (s *Service) List(ctx context.Context, filter models.JobFilter) ([]*models.Job, error) {
	return s.repo.List(ctx, filter)
}

func (s *Service) Cancel(ctx context.Context, id string) error {
	return s.repo.Cancel(ctx, id)
}

func (s *Service) Retry(ctx context.Context, id string) (*models.Job, error) {
	return s.repo.Retry(ctx, id)
}

func (s *Service) Progress(ctx context.Context, id string, progress int, message string) error {
	return s.repo.SetProgress(ctx, id, progress, message)
}

func (s *Service) Complete(ctx context.Context, id string, result map[string]any) error {
	return s.repo.Complete(ctx, id, result)
}

func (s *Service) Fail(ctx context.Context, id string, err error) error {
	return s.repo.Fail(ctx, id, err.Error())
}

func (s *Service) AddEvent(ctx context.Context, jobID string, eventType string, message string, data map[string]any) error {
	return s.repo.AddEvent(ctx, jobID, eventType, message, data)
}

func (s *Service) ListEvents(ctx context.Context, jobID string) ([]models.JobEvent, error) {
	return s.repo.ListEvents(ctx, jobID)
}

func (s *Service) RequeueExpiredLeases(ctx context.Context) error {
	return s.repo.RequeueExpiredLeases(ctx)
}

func generateJobID() string {
	return fmt.Sprintf("job_%d_%s", time.Now().UnixNano(), randomString(8))
}

func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%0*x", n, time.Now().UnixNano())
	}
	return hex.EncodeToString(b)[:n]
}
