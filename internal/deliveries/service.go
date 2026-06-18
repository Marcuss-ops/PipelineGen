package deliveries

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"github.com/Marcuss-ops/PipelineGen/pkg/hashutil"
)

// Service manages the delivery lifecycle for artifacts.
type Service struct {
	repo      Repository
	providers map[string]Provider
	log       *zap.Logger
}

// NewService creates a new delivery service.
func NewService(repo Repository, log *zap.Logger) *Service {
	if log == nil {
		log = zap.NewNop()
	}
	return &Service{
		repo:      repo,
		providers: make(map[string]Provider),
		log:       log,
	}
}

// RegisterProvider adds a provider to the service.
func (s *Service) RegisterProvider(p Provider) {
	s.providers[p.Name()] = p
	s.log.Info("delivery provider registered", zap.String("provider", p.Name()))
}

// Enqueue creates a new PENDING delivery for an artifact.
func (s *Service) Enqueue(ctx context.Context, artifactID, provider, targetID string) (*Delivery, error) {
	id := "dlv_" + hashutil.RandomString(16)

	now := time.Now().UTC()
	d := &Delivery{
		ID:           id,
		ArtifactID:   artifactID,
		TargetID:     targetID,
		Provider:     provider,
		Status:       StatusPending,
		MaxAttempts:  3,
		NextAttemptAt: &now,
	}

	if err := s.repo.Create(ctx, d); err != nil {
		return nil, fmt.Errorf("deliveries: enqueue: %w", err)
	}

	s.log.Info("delivery enqueued",
		zap.String("id", id),
		zap.String("artifact_id", artifactID),
		zap.String("provider", provider),
	)
	return d, nil
}

// ClaimNext claims the next pending delivery for a worker.
func (s *Service) ClaimNext(ctx context.Context, workerID string, leaseTTL time.Duration) (*Delivery, error) {
	return s.repo.ClaimNext(ctx, workerID, leaseTTL)
}

// Execute runs a delivery and updates its status.
func (s *Service) Execute(ctx context.Context, d *Delivery, getReader func(ctx context.Context) (ProviderRequest, error)) error {
	p, ok := s.providers[d.Provider]
	if !ok {
		return fmt.Errorf("deliveries: unknown provider: %s", d.Provider)
	}

	// Transition to RUNNING
	if err := s.repo.UpdateStatus(ctx, d.ID, StatusRunning, "", "", ""); err != nil {
		return fmt.Errorf("deliveries: start %s: %w", d.ID, err)
	}
	d.Status = StatusRunning

	req, err := getReader(ctx)
	if err != nil {
		s.handleFailure(ctx, d, err, p)
		return fmt.Errorf("deliveries: prepare request: %w", err)
	}
	req.DeliveryID = d.ID
	req.ArtifactID = d.ArtifactID

	// Execute the provider
	result, err := p.Deliver(ctx, req)
	if err != nil {
		s.handleFailure(ctx, d, err, p)
		return err
	}

	// Mark SUCCEEDED
	if err := s.repo.UpdateStatus(ctx, d.ID, StatusSucceeded, result.RemoteID, result.RemoteURL, ""); err != nil {
		return fmt.Errorf("deliveries: complete %s: %w", d.ID, err)
	}

	s.log.Info("delivery succeeded",
		zap.String("id", d.ID),
		zap.String("provider", d.Provider),
		zap.String("remote_id", result.RemoteID),
	)
	return nil
}

// handleFailure processes a delivery failure and decides retry vs failed.
func (s *Service) handleFailure(ctx context.Context, d *Delivery, err error, p Provider) {
	fc := p.ClassifyError(err)
	d.AttemptCount++

	s.log.Warn("delivery attempt failed",
		zap.String("id", d.ID),
		zap.Int("attempt", d.AttemptCount),
		zap.Int("failure_class", int(fc)),
		zap.Error(err),
	)

	switch fc {
	case FailureTemporary:
		if d.AttemptCount < d.MaxAttempts {
			nextAttempt := time.Now().UTC().Add(s.backoff(d.AttemptCount))
			// Update with retry info
			_ = s.repo.UpdateStatus(ctx, d.ID, StatusRetryWait, "", "", err.Error())
			return
		}
		// Fall through to permanent failure
		_ = s.repo.UpdateStatus(ctx, d.ID, StatusFailed, "", "", err.Error())
	case FailureAuth:
		_ = s.repo.UpdateStatus(ctx, d.ID, StatusBlockedAuth, "", "", err.Error())
	case FailurePermanent:
		_ = s.repo.UpdateStatus(ctx, d.ID, StatusFailed, "", "", err.Error())
	}
}

// backoff returns exponential backoff with jitter.
func (s *Service) backoff(attempt int) time.Duration {
	delays := []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute}
	if attempt <= len(delays) {
		return delays[attempt-1]
	}
	return 30 * time.Minute
}

// Get retrieves a delivery by ID.
func (s *Service) Get(ctx context.Context, id string) (*Delivery, error) {
	return s.repo.Get(ctx, id)
}

// ListByArtifact returns all deliveries for an artifact.
func (s *Service) ListByArtifact(ctx context.Context, artifactID string) ([]Delivery, error) {
	return s.repo.ListByArtifact(ctx, artifactID)
}

// RequeueStale returns stale deliveries to PENDING.
func (s *Service) RequeueStale(ctx context.Context, now time.Time, limit int) ([]Delivery, error) {
	return s.repo.RequeueStale(ctx, now, limit)
}

// ProviderRequest is the data needed by a provider to deliver an artifact.
type ProviderRequest struct {
	DeliveryID string
	ArtifactID string
	StorageKey string
	SHA256     string
	SizeBytes  int64
	MimeType   string
	LocalPath  string
}
