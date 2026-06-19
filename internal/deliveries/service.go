package deliveries

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	hashutil "github.com/Marcuss-ops/PipelineGen/internal/platform/files"
)

// Service manages the delivery lifecycle for artifacts.
type Service struct {
	repo      Repository
	providers map[string]Provider
	reader    ArtifactReader
	log       *zap.Logger
}

// NewService creates a new delivery service with an ArtifactReader for content access.
func NewService(repo Repository, reader ArtifactReader, log *zap.Logger) *Service {
	if log == nil {
		log = zap.NewNop()
	}
	return &Service{
		repo:      repo,
		providers: make(map[string]Provider),
		reader:    reader,
		log:       log,
	}
}

// RegisterProvider adds a provider to the service.
func (s *Service) RegisterProvider(p Provider) {
	s.providers[p.Name()] = p
	s.log.Info("delivery provider registered", zap.String("provider", p.Name()))
}

// Enqueue creates a new PENDING delivery for an artifact to a destination.
func (s *Service) Enqueue(ctx context.Context, artifactID, destinationID, provider string) (*Delivery, error) {
	// Idempotency check: if a delivery already exists for this (artifact, destination, provider),
	// return it regardless of status (PENDING/RETRY_WAIT/terminal — avoids duplicate work).
	if existing, _ := s.repo.FindByIdempotencyKey(ctx, computeIdempotencyKey(artifactID, destinationID, provider)); existing != nil {
		s.log.Info("delivery already exists (idempotent)",
			zap.String("id", existing.ID),
			zap.String("status", string(existing.Status)),
		)
		return existing, nil
	}

	id := "dlv_" + hashutil.RandomString(16)
	now := time.Now().UTC()
	d := &Delivery{
		ID:            id,
		ArtifactID:    artifactID,
		DestinationID: destinationID,
		Provider:      provider,
		Status:        StatusPending,
		MaxAttempts:   5,
		NextAttemptAt: &now,
	}

	if err := s.repo.Create(ctx, d); err != nil {
		return nil, fmt.Errorf("deliveries: enqueue: %w", err)
	}

	s.log.Info("delivery enqueued",
		zap.String("id", id),
		zap.String("artifact_id", artifactID),
		zap.String("destination_id", destinationID),
		zap.String("provider", provider),
	)
	return d, nil
}

// ClaimNext claims the next pending delivery for a worker.
func (s *Service) ClaimNext(ctx context.Context, workerID string, leaseTTL time.Duration) (*Delivery, error) {
	return s.repo.ClaimNext(ctx, workerID, leaseTTL)
}

// Execute runs a delivery using the configured ArtifactReader and provider.
func (s *Service) Execute(ctx context.Context, d *Delivery) error {
	p, ok := s.providers[d.Provider]
	if !ok {
		return fmt.Errorf("deliveries: unknown provider: %s", d.Provider)
	}

	// Load destination for provider config
	dest, err := s.repo.GetDestination(ctx, d.DestinationID)
	if err != nil {
		return fmt.Errorf("deliveries: get destination: %w", err)
	}
	if dest == nil {
		return fmt.Errorf("deliveries: destination %s not found", d.DestinationID)
	}

	// Build ArtifactDescriptor from delivery record fields.
	// StorageKey and SHA256 are populated at Enqueue time from the artifact.
	artifact := ArtifactDescriptor{
		ArtifactID: d.ArtifactID,
		StorageKey: d.StorageKey,
		ObjectInfo: ObjectInfo{
			SHA256:    d.SHA256,
			SizeBytes: d.SizeBytes,
			MimeType:  d.MimeType,
		},
	}

	// Execute the provider with ArtifactReader
	result, err := p.Deliver(ctx, artifact, s.reader, *dest)
	if err != nil {
		return s.handleFailure(ctx, d, err, p)
	}

	// Atomic complete
	return s.repo.CompleteDelivery(ctx, CompleteDeliveryCommand{
		DeliveryID: d.ID,
		LockedBy:   d.LockedBy,
		RemoteID:   result.RemoteID,
		RemoteURL:  result.RemoteURL,
	})
}

// handleFailure processes a delivery failure and atomically updates status.
func (s *Service) handleFailure(ctx context.Context, d *Delivery, err error, p Provider) error {
	fc := p.ClassifyError(err)

	s.log.Warn("delivery attempt failed",
		zap.String("id", d.ID),
		zap.Int("attempt", d.AttemptCount+1),
		zap.Int("failure_class", int(fc)),
		zap.Error(err),
	)

	switch fc {
	case FailureTemporary:
		if d.AttemptCount < d.MaxAttempts {
			nextAttempt := time.Now().UTC().Add(s.backoff(d.AttemptCount + 1))
			return s.repo.RetryDelivery(ctx, RetryDeliveryCommand{
				DeliveryID:   d.ID,
				LockedBy:     d.LockedBy,
				NextAttemptAt: nextAttempt,
				ErrorMessage: err.Error(),
			})
		}
		return s.repo.FailDelivery(ctx, FailDeliveryCommand{
			DeliveryID:   d.ID,
			LockedBy:     d.LockedBy,
			ErrorMessage: err.Error(),
		})

	case FailureAuth:
		return s.repo.BlockDeliveryAuth(ctx, BlockAuthCommand{
			DeliveryID:   d.ID,
			LockedBy:     d.LockedBy,
			ErrorMessage: err.Error(),
		})

	case FailurePermanent:
		return s.repo.FailDelivery(ctx, FailDeliveryCommand{
			DeliveryID:   d.ID,
			LockedBy:     d.LockedBy,
			ErrorMessage: err.Error(),
		})
	}
	return err
}

// backoff returns exponential backoff.
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
