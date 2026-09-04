package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/pkg/background"
	corid "github.com/Marcuss-ops/PipelineGen/pkg/corid"
)

const correlationLookupTimeout = 2 * time.Second

// Service owns enqueue, idempotency and queue-admission policy. It depends on
// the kernel broker contract plus narrow retry/consumer registries; it never
// imports the root jobs package or a concrete persistence driver.
type Service struct {
	repo      job.JobBroker
	retries   MaxRetriesResolver
	consumers ConsumerBindings
	log       *zap.Logger
}

func NewService(repo job.JobBroker, retries MaxRetriesResolver, consumers ConsumerBindings, log *zap.Logger) *Service {
	if log == nil {
		log = zap.NewNop()
	}
	return &Service{repo: repo, retries: retries, consumers: consumers, log: log}
}

// Enqueue performs canonical queue admission and idempotent persistence.
// Persistence adapters classify storage-specific duplicate violations as
// job.ErrDuplicate; this layer only understands that kernel sentinel.
func (s *Service) Enqueue(ctx context.Context, req *job.EnqueueRequest) (ret *job.Job, retErr error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("queue enqueue: repository is required")
	}

	parentRun := kernobs.FromContext(ctx)
	created := false
	if parentRun != nil {
		defer func() {
			switch {
			case created && ret != nil:
				parentRun.RegisterChild(&kernobs.RunReport{JobID: ret.ID, JobType: ret.Type, Status: kernobs.StatusRunning})
			case !created && retErr != nil && req != nil:
				parentRun.RegisterChild(&kernobs.RunReport{JobType: req.Type, Status: kernobs.StatusFailed})
			}
		}()
	}

	if err := ValidateEnqueueRequest(req); err != nil {
		return nil, err
	}

	correlationID := req.CorrelationID
	if correlationID == "" {
		if cid := corid.FromContext(ctx); cid != "" {
			correlationID = cid
		}
	}

	if req.ActiveKey != "" {
		existing, err := s.repo.FindActiveByKey(ctx, req.ActiveKey)
		if err != nil {
			return nil, fmt.Errorf("failed to check existing job: %w", err)
		}
		if existing != nil && !existing.IsTerminal() {
			s.log.Info("returning existing job with same active key", zap.String("job_id", existing.ID))
			return existing, nil
		}
	}

	if correlationID != "" {
		existing, err := s.findExistingByCorrelation(ctx, req.Type, correlationID)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			s.log.Info("returning existing job with same (type, correlation_id)", zap.String("job_id", existing.ID), zap.String("type", req.Type), zap.String("correlation_id", correlationID))
			return existing, nil
		}
	}

	if req.ClientID != "" && req.IdempotencyKey != "" {
		existing, err := s.findExistingByClientAndIdempotency(ctx, req.ClientID, req.IdempotencyKey)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			s.log.Info("returning existing job with same (client_id, idempotency_key)", zap.String("job_id", existing.ID), zap.String("client_id", req.ClientID), zap.String("idempotency_key", req.IdempotencyKey))
			return existing, nil
		}
	}

	var payload json.RawMessage
	if req.Payload != nil {
		payloadBytes, err := json.Marshal(req.Payload)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal payload: %w", err)
		}
		if len(payloadBytes) > MaxPayloadSize {
			return nil, fmt.Errorf("payload size %d exceeds maximum %d bytes", len(payloadBytes), MaxPayloadSize)
		}
		payload = payloadBytes
	}

	now := time.Now()
	j := &job.Job{
		ID:             GenerateJobID(),
		Type:           req.Type,
		Status:         job.StatusQueued,
		Priority:       req.Priority,
		Project:        req.Project,
		VideoName:      req.VideoName,
		Payload:        payload,
		RetryCount:     0,
		MaxRetries:     req.MaxRetries,
		Progress:       0,
		CreatedAt:      now,
		UpdatedAt:      now,
		ActiveKey:      req.ActiveKey,
		CorrelationID:  correlationID,
		ClientID:       req.ClientID,
		IdempotencyKey: req.IdempotencyKey,
	}

	if s.consumers != nil {
		if err := RequireConsumer(j.Type, s.consumers); err != nil {
			return nil, err
		}
	}

	maxRetries, err := ResolveMaxRetries(s.retries, j.Type, j.MaxRetries)
	if err != nil {
		return nil, err
	}
	j.MaxRetries = maxRetries

	if len(j.Payload) == 0 || string(j.Payload) == "null" {
		j.Payload = json.RawMessage("{}")
	}

	j.ParentJobID = job.ParentLinkFromPayload(j.Payload).ParentJobID
	j.RootJobID = j.ID
	if j.ParentJobID != "" {
		j.RootJobID = j.ParentJobID
		if parent, err := s.repo.Get(ctx, j.ParentJobID); err == nil && parent != nil && parent.RootJobID != "" {
			j.RootJobID = parent.RootJobID
		}
	}

	if err := s.repo.Create(ctx, j); err != nil {
		if errors.Is(err, job.ErrDuplicate) && (correlationID != "" || req.ActiveKey != "" || (req.ClientID != "" && req.IdempotencyKey != "")) {
			if correlationID != "" {
				if existing, findErr := s.findExistingByCorrelation(ctx, j.Type, correlationID); findErr == nil && existing != nil {
					s.log.Info("returning existing job by (type, correlation_id) after duplicate write", zap.String("job_id", existing.ID), zap.String("type", j.Type), zap.String("correlation_id", correlationID))
					return existing, nil
				}
			}
			if req.ActiveKey != "" {
				if existing, findErr := s.repo.FindActiveByKey(ctx, req.ActiveKey); findErr == nil && existing != nil && !existing.IsTerminal() {
					s.log.Info("returning existing job by active key after duplicate write", zap.String("job_id", existing.ID), zap.String("active_key", req.ActiveKey))
					return existing, nil
				}
			}
			if req.ClientID != "" && req.IdempotencyKey != "" {
				if existing, findErr := s.repo.FindByClientAndIdempotencyKey(ctx, req.ClientID, req.IdempotencyKey); findErr == nil && existing != nil {
					s.log.Info("returning existing job by (client_id, idempotency_key) after duplicate write", zap.String("job_id", existing.ID), zap.String("client_id", req.ClientID), zap.String("idempotency_key", req.IdempotencyKey))
					return existing, nil
				}
			}
			return nil, err
		}
		return nil, fmt.Errorf("failed to create job: %w", err)
	}
	created = true

	s.log.Info("job enqueued", zap.String("job_id", j.ID), zap.String("type", j.Type), zap.String("correlation_id", j.CorrelationID))
	return j, nil
}

func (s *Service) findExistingByCorrelation(ctx context.Context, jobType, correlationID string) (*job.Job, error) {
	if correlationID == "" {
		return nil, nil
	}
	lookupCtx, cancel := background.DetachWithTimeout(ctx, "jobs-correlation-lookup", correlationLookupTimeout)
	defer cancel()
	existing, err := s.repo.FindByTypeAndCorrelation(lookupCtx, jobType, correlationID)
	if err == nil {
		return existing, nil
	}
	if isTransientLookupError(err) {
		s.log.Warn("job correlation lookup unavailable; proceeding without pre-check", zap.String("type", jobType), zap.String("correlation_id", correlationID), zap.Error(err))
		return nil, nil
	}
	return nil, fmt.Errorf("failed to check existing job by correlation: %w", err)
}

func (s *Service) findExistingByClientAndIdempotency(ctx context.Context, clientID, idempotencyKey string) (*job.Job, error) {
	if clientID == "" || idempotencyKey == "" {
		return nil, nil
	}
	lookupCtx, cancel := background.DetachWithTimeout(ctx, "jobs-m2m-idempotency-lookup", correlationLookupTimeout)
	defer cancel()
	existing, err := s.repo.FindByClientAndIdempotencyKey(lookupCtx, clientID, idempotencyKey)
	if err == nil {
		return existing, nil
	}
	if isTransientLookupError(err) {
		s.log.Warn("job M2M idempotency lookup unavailable; proceeding without pre-check", zap.String("client_id", clientID), zap.String("idempotency_key", idempotencyKey), zap.Error(err))
		return nil, nil
	}
	return nil, fmt.Errorf("failed to check existing job by client+idempotency_key: %w", err)
}

func isTransientLookupError(err error) bool {
	return err != nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled))
}
