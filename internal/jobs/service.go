package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/media/models"
	jobsrepo "velox/go-master/internal/repository/jobs"
	"velox/go-master/pkg/corid"
	"velox/go-master/pkg/hashutil"
)

// MaxPayloadSize is the maximum allowed size for a serialized job payload in bytes.
const MaxPayloadSize = 1 << 20 // 1 MB

// Service manages job life cycle: enqueue, query, cancel.
type Service struct {
	repo       *jobsrepo.Repository
	dispatcher *Dispatcher
	log        *zap.Logger

	// enqueueMu serializes FindActiveByKey + Create to prevent the
	// race where two concurrent Enqueue calls both find no existing
	// job and then both insert a duplicate (punto 21).
	enqueueMu sync.Mutex
}

func NewService(repo *jobsrepo.Repository, dispatcher *Dispatcher, log *zap.Logger) *Service {
	return &Service{
		repo:       repo,
		dispatcher: dispatcher,
		log:        log,
	}
}

func (s *Service) RegisterHandler(jobType models.JobType, handler HandlerFunc) error {
	return s.dispatcher.Register(jobType, handler)
}

// validateEnqueueRequest checks the EnqueueRequest for common errors (punto 23).
func validateEnqueueRequest(req *EnqueueRequest) error {
	if req == nil {
		return fmt.Errorf("enqueue request is nil")
	}
	if req.Type == "" {
		return fmt.Errorf("job type is required")
	}
	if req.Priority < 0 {
		return fmt.Errorf("priority must be non-negative, got %d", req.Priority)
	}
	// MaxRetries < 0 is accepted as a sentinel for "explicitly zero retries".
	// The actual clamp to 0 happens after validation in Enqueue.
	if req.MaxRetries < -1 {
		return fmt.Errorf("max_retries must be >= -1, got %d", req.MaxRetries)
	}
	if len(req.Payload) > MaxPayloadSize {
		return fmt.Errorf("payload size %d exceeds maximum %d bytes", len(req.Payload), MaxPayloadSize)
	}
	return nil
}

func (s *Service) Enqueue(ctx context.Context, req *EnqueueRequest) (*models.Job, error) {
	if err := validateEnqueueRequest(req); err != nil {
		return nil, err
	}

	// Idempotency: auto-inject correlation_id from the request context if the
	// caller didn't set one explicitly. The X-Request-ID middleware
	// (internal/api/middleware/middleware.go::RequestID) sets this via
	// corid.WithCorrelationID, so two enqueues from the same client request
	// surface as one job instead of producing duplicate work — particularly
	// important for video/image generation, where a network-level retry
	// without idempotency would burn hours of compute and external API quota.
	if req.CorrelationID == "" {
		if cid := corid.FromContext(ctx); cid != "" {
			req.CorrelationID = cid
		}
	}

	// Serialize the active-key check+create to prevent duplicate insertion
	// under concurrent Enqueue calls with the same active key (punto 21).
	// The mutex also serializes the (type, correlation_id) dedup check below
	// so concurrent retries from the same client can't both observe "no
	// existing job" and race past the INSERT.
	s.enqueueMu.Lock()
	defer s.enqueueMu.Unlock()

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

	// Idempotency on (type, correlation_id): two enqueues from the same
	// client request (same X-Request-ID) must converge on the same job.
	// The check happens under enqueueMu so concurrent retries can't both
	// miss an existing row; the conditional UNIQUE INDEX on
	// (type, correlation_id) (see migrations/sqlite/036_job_idempotency.sql)
	// is the ultimate safety net handled below the INSERT.
	if req.CorrelationID != "" {
		existing, err := s.repo.FindByTypeAndCorrelation(ctx, req.Type, req.CorrelationID)
		if err != nil {
			return nil, fmt.Errorf("failed to check existing job by correlation: %w", err)
		}
		if existing != nil {
			s.log.Info("returning existing job with same (type, correlation_id)",
				zap.String("job_id", existing.ID),
				zap.String("type", string(req.Type)),
				zap.String("correlation_id", req.CorrelationID),
			)
			return existing, nil
		}
	}

	now := time.Now()

	var payload json.RawMessage
	if req.Payload != nil {
		payloadBytes, err := json.Marshal(req.Payload)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal payload: %w", err)
		}
		payload = payloadBytes
	}

	job := &models.Job{
		ID:            generateJobID(),
		Type:          req.Type,
		Status:        models.StatusQueued,
		Priority:      req.Priority,
		Project:       req.Project,
		VideoName:     req.VideoName,
		Payload:       payload,
		RetryCount:    0,
		MaxRetries:    req.MaxRetries,
		Progress:      0,
		CreatedAt:     now,
		UpdatedAt:     now,
		ActiveKey:     req.ActiveKey,
		CorrelationID: req.CorrelationID,
	}

	// Backward-compatible default: MaxRetries == 0 means 3 retries, matching
	// the historic behaviour. Callers that want zero retries must pass
	// MaxRetries: -1 (the validation check above ensures it's < 0, so
	// we reset -1 back to 0 here).
	if job.MaxRetries == 0 {
		job.MaxRetries = 3
	} else if job.MaxRetries < 0 {
		job.MaxRetries = 0 // sentinel for "explicitly zero retries"
	}

	if job.Payload == nil {
		job.Payload = json.RawMessage("{}")
	}

	if err := s.repo.Create(ctx, job); err != nil {
		// Idempotency safety net: if the (type, correlation_id) UNIQUE
		// collision happens — another enqueue beat us between the dedup
		// check above and this INSERT, or two distinct processes raced
		// past the goroutine-local mutex — fetch the winning row and
		// return it as if the caller had retried. The mutex in production
		// single-process deployments already prevents most races; this
		// branch covers multi-process / future multi-node setups.
		//
		// Match by broad UNIQUE-constraint substring rather than the exact
		// "jobs.type, jobs.correlation_id" wording: mattn/go-sqlite3 has
		// shifted message formatting across versions (lowercase verb,
		// reordered column list for multi-column collisions), and any
		// future UNIQUE index added to the jobs table should also be
		// eligible for the rescue — the FindByTypeAndCorrelation narrows
		// correctly when the row really exists.
		//
		// Note: callers retrying after a Completed/Failed/Cancelled job
		// will receive the existing terminal job back — idempotency here
		// means 'no new work'. To re-run a terminal job, call Retry(id).
		if job.CorrelationID != "" && strings.Contains(err.Error(), "UNIQUE constraint") {
			if existing, findErr := s.repo.FindByTypeAndCorrelation(ctx, job.Type, job.CorrelationID); findErr == nil && existing != nil {
				s.log.Info("returning existing job by (type, correlation_id) — caught race on UNIQUE constraint",
					zap.String("job_id", existing.ID),
					zap.String("type", string(job.Type)),
					zap.String("correlation_id", job.CorrelationID),
				)
				return existing, nil
			}
		}
		return nil, fmt.Errorf("failed to create job: %w", err)
	}

	s.log.Info("job enqueued",
		zap.String("job_id", job.ID),
		zap.String("type", string(job.Type)),
		zap.String("correlation_id", job.CorrelationID),
	)
	return job, nil
}

func (s *Service) Get(ctx context.Context, id string) (*models.Job, error) {
	return s.repo.Get(ctx, id)
}

func (s *Service) FindActiveByKey(ctx context.Context, activeKey string) (*models.Job, error) {
	return s.repo.FindActiveByKey(ctx, activeKey)
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

// SetRunning marks a queued job as running with optimistic-lock
// guarantees against double-claim by concurrent workers.
//
// Idempotency contract: if the job is already running (e.g. a caller
// re-invokes SetRunning in a recovery path), this method returns
// nil without firing a transition. The legacy SetStatusRunning was
// idempotent in the same way for backwards compatibility — a strict
// Transition(queued→running) would reject with
// `invalid transition: "running" → "running"`, which would break any
// caller that retries SetRunning. The short-circuit below preserves
// the legacy semantics.
//
// Returns:
//   - nil on success (transition fired) OR on idempotent no-op (job
//     already running);
//   - ErrOptimisticLockFailed if another worker raced us past the
//     Transition and bumped the revision;
//   - the wrapped state-machine error if the current status is
//     non-queued and non-running (e.g. cancelled / failed / completed).
func (s *Service) SetRunning(ctx context.Context, id string) error {
	job, err := s.repo.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("set running: load job: %w", err)
	}
	if job == nil {
		return fmt.Errorf("set running: job %s not found", id)
	}
	if job.Status == models.StatusRunning {
		// Idempotent: already running. Preserves legacy semantics so
		// any caller that double-invokes (e.g. recovery paths) does
		// not surface a state-machine error.
		return nil
	}
	_, err = s.repo.Transition(ctx, jobsrepo.TransitionRequest{
		JobID:            id,
		ExpectedRevision: job.Revision,
		ExpectedStatus:   job.Status,
		NewStatus:        models.StatusRunning,
		Updates: map[string]any{
			"started_at": time.Now(),
		},
	})
	return err
}

// Transition executes a status change on the underlying repository
// using the optimistic-lock contract (revision + expected status).
// This is the canonical primitive for advancing job lifecycle; all
// higher-level helpers (Complete, Fail, Cancel, Retry) are thin
// wrappers over this method, but new flows may call it directly.
func (s *Service) Transition(ctx context.Context, req jobsrepo.TransitionRequest) (*models.Job, error) {
	return s.repo.Transition(ctx, req)
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

// GetStats returns aggregated job statistics.
func (s *Service) GetStats(ctx context.Context) (*jobsrepo.JobStats, error) {
	return s.repo.GetStats(ctx)
}

func (s *Service) MarkStaleRunningJobsFailed(ctx context.Context, olderThan time.Duration) (int, error) {
	cutoff := time.Now().UTC().Add(-olderThan)
	return s.repo.MarkRunningJobsOlderThanFailed(ctx, cutoff, "stale job timeout")
}

func generateJobID() string {
	return fmt.Sprintf("job_%d_%s", time.Now().UnixNano(), hashutil.RandomString(8))
}
