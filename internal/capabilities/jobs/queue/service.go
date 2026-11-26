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
	// scheduler is the OPTIONAL deferred-scheduling port. It is wired only
	// when the composition root provides a job.ScheduleStore (the SQLite
	// jobs plane implements it). It is nil for callers/tests that never
	// schedule a job; an Enqueue with a future ScheduledAt on a Service
	// without this port fails closed rather than silently running early.
	scheduler job.ScheduleStore
}

// WithScheduler attaches the deferred-scheduling port. Chainable; returns
// the receiver so composition can write
// queue.NewService(...).WithScheduler(store).
func (s *Service) WithScheduler(store job.ScheduleStore) *Service {
	if s != nil {
		s.scheduler = store
	}
	return s
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
//
// The stages are deliberately named: validation/identity, payload, scheduling,
// job construction, admission policy, persistence and duplicate recovery. Each
// stage preserves the order and error text of the original single body.
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

	correlationID := resolveCorrelationID(ctx, req)
	if existing, err := s.findExistingBeforeCreate(ctx, req, correlationID); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
	}

	payload, err := encodeEnqueuePayload(req)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	scheduled := req.ScheduledAt != nil && req.ScheduledAt.After(now)
	if scheduled && s.scheduler == nil {
		// Fail closed: running a future-dated job immediately would violate
		// the caller's request. The composition root must wire the
		// schedule store (queue.Service.WithScheduler).
		return nil, fmt.Errorf("job %q requested scheduled_at=%s but the schedule store is not wired", req.Type, req.ScheduledAt.UTC().Format(time.RFC3339))
	}

	initialStatus, scheduling, err := buildSchedulingDescriptor(req, payload, now)
	if err != nil {
		return nil, err
	}

	j := newJobFromRequest(req, correlationID, payload, scheduling, initialStatus, now)
	if err := s.applyConsumerAndRetryPolicy(j); err != nil {
		return nil, err
	}
	s.resolveParentRoot(ctx, j)

	createErr := s.persistJob(ctx, j, req, scheduled)
	if createErr != nil {
		if errors.Is(createErr, job.ErrDuplicate) && hasRecoverableDuplicateKey(req, correlationID) {
			if existing := s.findExistingAfterDuplicate(ctx, req, correlationID); existing != nil {
				return existing, nil
			}
			return nil, createErr
		}
		return nil, fmt.Errorf("failed to create job: %w", createErr)
	}
	created = true

	s.log.Info("job enqueued", zap.String("job_id", j.ID), zap.String("type", j.Type), zap.String("correlation_id", j.CorrelationID))
	return j, nil
}

// resolveCorrelationID prefers the explicit request value and falls back to the
// context correlation id. It performs no I/O.
func resolveCorrelationID(ctx context.Context, req *job.EnqueueRequest) string {
	correlationID := req.CorrelationID
	if correlationID == "" {
		if cid := corid.FromContext(ctx); cid != "" {
			correlationID = cid
		}
	}
	return correlationID
}

// findExistingBeforeCreate runs the three idempotency pre-checks in their
// original priority order. Active-key lookup errors are fatal; correlation and
// client/idempotency lookups apply the transient policy in their helpers.
func (s *Service) findExistingBeforeCreate(ctx context.Context, req *job.EnqueueRequest, correlationID string) (*job.Job, error) {
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

	return nil, nil
}

// encodeEnqueuePayload marshals the optional payload and enforces the hard
// payload budget before the job reaches persistence.
func encodeEnqueuePayload(req *job.EnqueueRequest) (json.RawMessage, error) {
	if req.Payload == nil {
		return nil, nil
	}
	payloadBytes, err := json.Marshal(req.Payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}
	if len(payloadBytes) > MaxPayloadSize {
		return nil, fmt.Errorf("payload size %d exceeds maximum %d bytes", len(payloadBytes), MaxPayloadSize)
	}
	return payloadBytes, nil
}

// buildSchedulingDescriptor derives the canonical scheduling projection and
// the initial status. The render_phase/lane agreement is checked here because
// it is part of the same wire contract.
func buildSchedulingDescriptor(req *job.EnqueueRequest, payload json.RawMessage, now time.Time) (job.Status, job.SchedulingDescriptor, error) {
	initialStatus := job.StatusQueued
	if req.ScheduledAt != nil && req.ScheduledAt.After(now) {
		initialStatus = job.StatusScheduled
	}
	scheduling := job.SchedulingDescriptor{JobType: req.Type, Priority: req.Priority, AvailableAt: now}
	if req.ScheduledAt != nil {
		scheduling.AvailableAt = req.ScheduledAt.UTC()
	}
	if req.Scheduling != nil {
		scheduling.ResourceClass = req.Scheduling.ResourceClass
		scheduling.ExecutionLane = req.Scheduling.ExecutionLane
		if req.Scheduling.JobType != "" && req.Scheduling.JobType != req.Type {
			return "", job.SchedulingDescriptor{}, fmt.Errorf("scheduling descriptor job_type %q does not match enqueue type %q", req.Scheduling.JobType, req.Type)
		}
		if req.Scheduling.Priority != 0 && req.Scheduling.Priority != req.Priority {
			return "", job.SchedulingDescriptor{}, fmt.Errorf("scheduling descriptor priority %d does not match enqueue priority %d", req.Scheduling.Priority, req.Priority)
		}
		if !req.Scheduling.AvailableAt.IsZero() {
			if req.ScheduledAt == nil {
				return "", job.SchedulingDescriptor{}, fmt.Errorf("scheduling descriptor available_at requires scheduled_at")
			}
			if !req.Scheduling.AvailableAt.Equal(req.ScheduledAt.UTC()) {
				return "", job.SchedulingDescriptor{}, fmt.Errorf("scheduling descriptor available_at does not match scheduled_at")
			}
			scheduling.AvailableAt = req.Scheduling.AvailableAt.UTC()
		}
	}
	if len(payload) > 0 {
		var hints map[string]json.RawMessage
		if err := json.Unmarshal(payload, &hints); err == nil {
			if rawPhase, ok := hints["render_phase"]; ok {
				var phase string
				if err := json.Unmarshal(rawPhase, &phase); err != nil {
					return "", job.SchedulingDescriptor{}, fmt.Errorf("payload render_phase must be a string: %w", err)
				}
				if phase != "" && scheduling.ExecutionLane != phase {
					return "", job.SchedulingDescriptor{}, fmt.Errorf("scheduling execution_lane %q does not match payload render_phase %q", scheduling.ExecutionLane, phase)
				}
			}
		}
	}
	if err := scheduling.Validate(); err != nil {
		return "", job.SchedulingDescriptor{}, fmt.Errorf("invalid scheduling descriptor: %w", err)
	}
	return initialStatus, scheduling, nil
}

// newJobFromRequest projects the validated request into the persisted job
// shape. No policy decisions are made here.
func newJobFromRequest(req *job.EnqueueRequest, correlationID string, payload json.RawMessage, scheduling job.SchedulingDescriptor, initialStatus job.Status, now time.Time) *job.Job {
	return &job.Job{
		ID:             GenerateJobID(),
		Type:           req.Type,
		Status:         initialStatus,
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
		Scheduling:     scheduling,
	}
}

// applyConsumerAndRetryPolicy enforces queue admission (consumer binding and
// retry budget) and normalizes an absent payload to the canonical empty object.
func (s *Service) applyConsumerAndRetryPolicy(j *job.Job) error {
	if s.consumers != nil {
		if err := RequireConsumer(j.Type, s.consumers); err != nil {
			return err
		}
	}
	maxRetries, err := ResolveMaxRetries(s.retries, j.Type, j.MaxRetries)
	if err != nil {
		return err
	}
	j.MaxRetries = maxRetries
	if len(j.Payload) == 0 || string(j.Payload) == "null" {
		j.Payload = json.RawMessage("{}")
	}
	return nil
}

// resolveParentRoot derives the parent/root lineage from the payload. A parent
// read failure is deliberately non-fatal: the child keeps the parent id as its
// root, exactly as before.
func (s *Service) resolveParentRoot(ctx context.Context, j *job.Job) {
	j.ParentJobID = job.ParentLinkFromPayload(j.Payload).ParentJobID
	j.RootJobID = j.ID
	if j.ParentJobID != "" {
		j.RootJobID = j.ParentJobID
		if parent, err := s.repo.Get(ctx, j.ParentJobID); err == nil && parent != nil && parent.RootJobID != "" {
			j.RootJobID = parent.RootJobID
		}
	}
}

// persistJob routes to the schedule store or the regular broker. The caller
// has already checked that a scheduled request has a scheduler.
func (s *Service) persistJob(ctx context.Context, j *job.Job, req *job.EnqueueRequest, scheduled bool) error {
	if scheduled {
		return s.scheduler.CreateScheduled(ctx, j, *req.ScheduledAt)
	}
	return s.repo.Create(ctx, j)
}

// hasRecoverableDuplicateKey reports whether a duplicate write can be resolved
// by looking the existing job up again.
func hasRecoverableDuplicateKey(req *job.EnqueueRequest, correlationID string) bool {
	return correlationID != "" || req.ActiveKey != "" || (req.ClientID != "" && req.IdempotencyKey != "")
}

// findExistingAfterDuplicate resolves a duplicate Create by re-reading the same
// keys in their original priority order. Lookup errors are ignored here: the
// caller falls back to the original create error, which is the more useful
// signal than a secondary read failure.
func (s *Service) findExistingAfterDuplicate(ctx context.Context, req *job.EnqueueRequest, correlationID string) *job.Job {
	if correlationID != "" {
		if existing, err := s.findExistingByCorrelation(ctx, req.Type, correlationID); err == nil && existing != nil {
			s.log.Info("returning existing job by (type, correlation_id) after duplicate write", zap.String("job_id", existing.ID), zap.String("type", req.Type), zap.String("correlation_id", correlationID))
			return existing
		}
	}
	if req.ActiveKey != "" {
		if existing, err := s.repo.FindActiveByKey(ctx, req.ActiveKey); err == nil && existing != nil && !existing.IsTerminal() {
			s.log.Info("returning existing job by active key after duplicate write", zap.String("job_id", existing.ID), zap.String("active_key", req.ActiveKey))
			return existing
		}
	}
	if req.ClientID != "" && req.IdempotencyKey != "" {
		if existing, err := s.repo.FindByClientAndIdempotencyKey(ctx, req.ClientID, req.IdempotencyKey); err == nil && existing != nil {
			s.log.Info("returning existing job by (client_id, idempotency_key) after duplicate write", zap.String("job_id", existing.ID), zap.String("client_id", req.ClientID), zap.String("idempotency_key", req.IdempotencyKey))
			return existing
		}
	}
	return nil
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
