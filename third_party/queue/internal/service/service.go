// Package service implements the job queue business logic, sitting between
// the HTTP server and the storage backend. It owns rules such as input
// validation and emits operational metrics; storage concerns stay in the
// repository packages.
//
//	HTTP Server → Service → JobRepository → (memory | postgres)
package service

import (
	"context"
	"fmt"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/metrics"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository"
	"sync"
	"time"
)

// Service is the job queue application service.
type Service struct {
	repo       repository.JobRepository
	workerRepo repository.WorkerRepository
	staleAfter time.Duration
	retry      RetryConfig
	metrics    *metrics.Metrics
	notify     *Notifier

	// clock is the seam the worker-liveness classification reads (see
	// workers.go). nil means time.Now(); production never sets it.
	clock func() time.Time

	// pendingGaugeMu guards observePending's throttle clock. The pending
	// gauge reads the whole render_jobs table (count(*) FILTER ...) on every
	// call; throttling it to once per interval keeps submit/claim/complete
	// off the full-scan path on large tables. State-changing paths still
	// notify claim waiters through s.notify, which is what latency depends
	// on — the gauge is observability, not correctness.
	pendingGaugeMu   sync.Mutex
	pendingGaugeLast time.Time
}

// pendingGaugeInterval is the minimum spacing between full-table gauge reads.
const pendingGaugeInterval = 10 * time.Second

// New creates a service backed by the given repository.
func New(repo repository.JobRepository) *Service {
	return &Service{repo: repo, notify: NewNotifier()}
}

// SetMetrics attaches optional Prometheus metrics. When nil, metric emission
// is a no-op so tests and lightweight deployments can run without them.
func (s *Service) SetMetrics(m *metrics.Metrics) {
	s.metrics = m
}

// SetRequeueRetry configures retry-with-backoff for RequeueExpired. The zero
// RetryConfig (the default) performs a single attempt with no retry.
func (s *Service) SetRequeueRetry(cfg RetryConfig) {
	s.retry = cfg
}

// SetWorkerRepository wires the worker registry and the heartbeat-staleness
// window used to classify worker liveness. A nil repository disables the worker
// surface and its metrics.
//
// staleAfter is the whole window: a worker is DEGRADED past a third of it,
// STALE past two thirds, and DEAD beyond it (model.WorkerLivenessWindow is the
// authority). A non-positive window classifies every worker as dead rather than
// treating "no window" as "no limits".
func (s *Service) SetWorkerRepository(repo repository.WorkerRepository, staleAfter time.Duration) {
	s.workerRepo = repo
	s.staleAfter = staleAfter
}

// Submit enqueues a job. The ID is required and must be unique.
func (s *Service) Submit(ctx context.Context, job model.Job) error {
	_, _, err := s.SubmitIdempotent(ctx, job)
	return err
}

// SubmitBatch creates a complete anchor+children family atomically. The
// repository capability is mandatory: silently falling back to a loop would
// reintroduce the anchor-claim race this API exists to remove.
func (s *Service) SubmitBatch(ctx context.Context, jobs []model.Job) error {
	if len(jobs) == 0 {
		return fmt.Errorf("job batch is empty")
	}
	b, ok := s.repo.(repository.BatchRepository)
	if !ok {
		return fmt.Errorf("queue repository does not support atomic job batches")
	}
	if err := b.SubmitBatch(ctx, jobs); err != nil {
		return err
	}
	s.observePending(ctx)
	s.notify.Notify()
	return nil
}

// SubmitIdempotent returns the canonical job for an idempotency key. created
// is false when the request is a retry of an existing logical job.
func (s *Service) SubmitIdempotent(ctx context.Context, job model.Job) (*model.Job, bool, error) {
	if job.ID == "" {
		return nil, false, fmt.Errorf("job id is required")
	}
	if idem, ok := s.repo.(repository.IdempotencyRepository); ok {
		canonical, created, err := idem.SubmitIdempotent(ctx, job)
		if err != nil {
			return nil, false, err
		}
		if created {
			s.observePending(ctx)
			s.notify.Notify()
		}
		return canonical, created, nil
	}
	if err := s.repo.Submit(ctx, job); err != nil {
		return nil, false, err
	}
	s.observePending(ctx)
	s.notify.Notify()
	canonical, err := s.repo.Get(ctx, job.ID)
	return canonical, true, err
}

// Notify returns the service's wake-up channel stream. The server uses it to
// long-poll claims: the signal carries no data and never assigns work, it only
// wakes a waiting claim to re-run the atomic claim against the store.
func (s *Service) Notify() *Notifier { return s.notify }

func (s *Service) ClaimFinalization(ctx context.Context, parentID, workerID string) (*model.Job, bool, error) {
	job, claimed, err := s.repo.ClaimFinalization(ctx, parentID, workerID)
	if claimed {
		s.notify.Notify()
	}
	return job, claimed, err
}

func (s *Service) Children(ctx context.Context, parentID string) ([]*model.Job, error) {
	return s.repo.Children(ctx, parentID)
}

func (s *Service) RecoverableParents(ctx context.Context) ([]string, error) {
	return s.repo.RecoverableParents(ctx)
}

// ByParent returns every job submitted under one parent_job_id (the run-scoped
// read: "what did this run enqueue?"), in submission order.
func (s *Service) ByParent(ctx context.Context, parentID string) ([]*model.Job, error) {
	return s.repo.ByParent(ctx, parentID)
}

// Get returns the current state of a job, including its artifact when done.
func (s *Service) Get(ctx context.Context, id string) (*model.Job, error) {
	return s.repo.Get(ctx, id)
}

// Claim atomically claims the next pending job for a worker, returning the job
// and its lease duration. It returns a nil job when the queue is empty.
func (s *Service) Claim(ctx context.Context, workerID string) (*model.Job, time.Duration, error) {
	return s.ClaimState(ctx, workerID, "")
}

func (s *Service) ClaimState(ctx context.Context, workerID string, state model.State) (*model.Job, time.Duration, error) {
	if workerID == "" {
		return nil, 0, fmt.Errorf("worker id is required")
	}
	job, lease, err := s.repo.ClaimState(ctx, workerID, state)
	if err != nil {
		return nil, 0, err
	}
	if job != nil && s.metrics != nil && !job.QueuedAt.IsZero() {
		s.metrics.QueueWait.Observe(time.Since(job.QueuedAt).Seconds())
	}
	s.observePending(ctx)
	return job, lease, nil
}

// WaitAndClaim long-polls for a claimable job. It re-runs the atomic claim
// immediately on every wake-up and falls back to a bounded tick so spurious
// signal loss (or a repository without notifications) cannot stall a worker.
// The wait never assigns work: every successful claim goes through ClaimState
// and the store remains the single source of truth.
func (s *Service) WaitAndClaim(ctx context.Context, workerID string, state model.State, maxWait time.Duration) (*model.Job, time.Duration, error) {
	if workerID == "" {
		return nil, 0, fmt.Errorf("worker id is required")
	}
	if maxWait < 0 {
		return nil, 0, fmt.Errorf("max wait must be >= 0")
	}
	if maxWait == 0 {
		maxWait = 25 * time.Second
	}
	job, lease, err := s.ClaimState(ctx, workerID, state)
	if err != nil || job != nil {
		return job, lease, err
	}
	deadline := time.NewTimer(maxWait)
	defer deadline.Stop()
	wake := s.notify.Done()
	// Fallback re-poll delay grows after every empty poll so an idle fleet of
	// waiting claims does not pound render_jobs with a fixed 1 Hz atomic-claim
	// baseline each; a wake-up signal (or a successful claim) resets it so the
	// worker stays responsive the moment work actually appears.
	pollDelay := time.Second
	const maxPollDelay = 10 * time.Second
	// Reused poll timer: time.After in this loop would allocate and leak a
	// timer per backoff step until it fires.
	pollTimer := time.NewTimer(pollDelay)
	defer pollTimer.Stop()
	for {
		select {
		case <-ctx.Done():
			s.notify.Forget(wake)
			return nil, 0, ctx.Err()
		case <-deadline.C:
			s.notify.Forget(wake)
			return nil, 0, nil
		case <-wake:
			job, lease, err := s.ClaimState(ctx, workerID, state)
			if err != nil || job != nil {
				return job, lease, err
			}
			// Signal consumed by a competing worker; stay responsive and keep
			// waiting at the fresh backoff.
			wake = s.notify.Done()
			pollDelay = time.Second
			if !pollTimer.Stop() {
				select {
				case <-pollTimer.C:
				default:
				}
			}
			pollTimer.Reset(pollDelay)
		case <-pollTimer.C:
			// Bounded fallback re-poll so a missed wake-up cannot stall a
			// worker until the deadline. Back off after every empty poll;
			// the deadline timer bounds the total wait regardless. The
			// re-poll is also the timeout fallback for single-wake: a waiter
			// whose wake was consumed by a competitor is re-driven here.
			job, lease, err := s.ClaimState(ctx, workerID, state)
			if err != nil || job != nil {
				s.notify.Forget(wake)
				return job, lease, err
			}
			pollDelay *= 2
			if pollDelay > maxPollDelay {
				pollDelay = maxPollDelay
			}
			pollTimer.Reset(pollDelay)
		}
	}
}

func (s *Service) Complete(ctx context.Context, id, workerID string, artifact model.Artifact) error {
	if err := validateArtifact(artifact); err != nil {
		return err
	}
	if err := s.repo.Complete(ctx, id, workerID, artifact); err != nil {
		return err
	}
	if s.metrics != nil {
		if job, err := s.repo.Get(ctx, id); err == nil && job != nil && !job.StartedAt.IsZero() {
			d := job.CompletedAt.Sub(job.StartedAt)
			if d <= 0 {
				d = time.Since(job.StartedAt)
			}
			s.metrics.RenderDuration.Observe(d.Seconds())
		}
	}
	s.observePending(ctx)
	return nil
}

// observePending refreshes the pending gauge from the repository snapshot,
// throttled to pendingGaugeInterval so high-frequency transitions (claim,
// complete, requeue across many workers) cannot turn the gauge into a
// full-table scan per request. State-changing paths still notify claim
// waiters through s.notify, which is what latency depends on — the gauge is
// observability, not correctness.
func (s *Service) observePending(ctx context.Context) {
	s.observePendingThrottled(ctx, false)
}

// observePendingThrottled is the shared implementation; force bypasses the
// throttle clock.
func (s *Service) observePendingThrottled(ctx context.Context, force bool) {
	if s.metrics == nil {
		return
	}
	// Read/advance the throttle clock under the lock, then fetch the stats
	// OUTSIDE it: repo.Stats() is a full-table COUNT(*) FILTER scan on
	// PostgreSQL, and every state-changing path (Claim, Complete, Fail,
	// Rendered, RequeueExpired) funnels through here. Holding the service-wide
	// mutex across that scan would serialize the whole queue hot path behind
	// observability on a large render_jobs table. A concurrent caller may now
	// pass the throttle and double-issue one scan; that is bounded, benign
	// contention, not a correctness hazard — the gauge is observability.
	s.pendingGaugeMu.Lock()
	if !force && !s.pendingGaugeLast.IsZero() && time.Since(s.pendingGaugeLast) < pendingGaugeInterval {
		s.pendingGaugeMu.Unlock()
		return
	}
	s.pendingGaugeLast = time.Now()
	s.pendingGaugeMu.Unlock()

	stats := s.repo.Stats(ctx)
	s.metrics.JobsPending.Set(float64(stats.Pending))
	// Same snapshot, same refresh cadence: the head-of-line age is the
	// backlog signal Depth alone cannot give an autoscaler, and reading it
	// here costs nothing beyond the already-issued COUNT(*) FILTER scan.
	s.metrics.OldestPending.Set(stats.OldestPendingSeconds)
}

// RefreshPendingGauge forces an immediate, unthrottled pending-gauge refresh.
//
// It exists for TESTS: production refreshes the gauge through the throttled
// path, and a test that waited out pendingGaugeInterval to observe one update
// would be slow and flaky. There is deliberately no admin endpoint calling it —
// an on-demand scrape of the gauge is a metrics concern, not a queue API — so
// do not describe it as production-wired.
func (s *Service) RefreshPendingGauge() {
	// Deliberately not request-scoped: this is the gauge-refresh seam tests
	// call directly, and it owns no caller context to propagate.
	s.observePendingThrottled(context.Background(), true)
}
