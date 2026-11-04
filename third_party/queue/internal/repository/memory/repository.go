// Package memory implements the in-memory job queue, used as the default
// (non-persistent) backend for local development and tests. It implements
// repository.JobRepository, so it can be swapped for the PostgreSQL backend.
//
// Jobs are pulled (claimed) by workers instead of being pushed. A claim
// carries a lease: if the worker dies before completing, the lease expires
// and the job is requeued for another worker.
//
// Every method accepts the caller's context first, for interface parity with the
// PostgreSQL backend. The in-memory store never blocks on I/O — an operation
// either returns immediately or fails immediately — so it has nothing to
// observe cancellation on, and the parameter is deliberately ignored (named
// `_`) rather than checked for tidiness. Ignoring it is not an oversight: a
// context check here would only add a branch that can never change the answer.
package memory

import (
	"container/list"
	"context"
	"fmt"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository"
	"sort"
	"sync"
	"time"
)

// Repository is a thread-safe in-memory job queue with lease expiry.
//
// mu is a RWMutex: the read-only projections (Get, Children, Stats) take it
// shared, so a dashboard polling the queue cannot serialize the claim path. The
// mutating transitions hold it exclusively. ClaimState snapshots its FIFO
// candidates under RLock and only takes the write lock for the CAS
// (re-validate + claim + O(1) list removal), so concurrent claims contend on a
// short critical section instead of a full scan+memmove.
type Repository struct {
	mu          sync.RWMutex
	jobs        map[string]*model.Job
	artifacts   map[string]model.Artifact
	order       *list.List // FIFO queue of pending job IDs (string elements); O(1) removal, no memmove
	orderIdx    map[string]*list.Element
	idem        map[string]string // idempotency key -> job ID (mirrors the postgres unique index)
	workers     map[string]model.Worker
	lease       time.Duration
	maxAttempts int

	// children indexes parent job ID -> its child IDs (mirrors the postgres
	// child lookup). ClaimState asks "does another job declare this id as its
	// parent?" once per pending candidate, and answering that by scanning every
	// job row made a single claim O(pending x jobs) — quadratic growth in the
	// one operation every worker performs in a loop. A job's parent is fixed at
	// submit time and no job is ever deleted, so the index is append-only and
	// never needs invalidation.
	children map[string]map[string]struct{}
}

// Compile-time check that Repository satisfies the repository contracts.
var (
	_ repository.JobRepository              = (*Repository)(nil)
	_ repository.WorkerRepository           = (*Repository)(nil)
	_ repository.IdempotencyRepository      = (*Repository)(nil)
	_ repository.BatchRepository            = (*Repository)(nil)
	_ repository.PermanentFailureRepository = (*Repository)(nil)
)

// New creates a queue with the given lease duration and max attempts per job.
func New(lease time.Duration, maxAttempts int) *Repository {
	return &Repository{
		jobs:        make(map[string]*model.Job),
		artifacts:   make(map[string]model.Artifact),
		order:       list.New(),
		orderIdx:    make(map[string]*list.Element),
		idem:        make(map[string]string),
		workers:     make(map[string]model.Worker),
		children:    make(map[string]map[string]struct{}),
		lease:       lease,
		maxAttempts: maxAttempts,
	}
}

// appendOrderLocked enqueues id at the FIFO tail. The caller holds s.mu for
// writing. Removal is O(1) via orderIdx, so the claim path never pays a slice
// memmove.
func (s *Repository) appendOrderLocked(id string) {
	el := s.order.PushBack(id)
	s.orderIdx[id] = el
}

// removeOrderLocked unlinks id's queue element in O(1). Stale duplicates (e.g.
// a cancelled job left in the queue) are skipped by the claim predicate, so
// removing the indexed element is sufficient.
func (s *Repository) removeOrderLocked(id string) {
	if el, ok := s.orderIdx[id]; ok {
		s.order.Remove(el)
		delete(s.orderIdx, id)
	}
}

// insertLocked stores an already-validated job, indexes its family and appends
// it to the FIFO. Every insert path (single, idempotent, batch) shares this
// write side.
func (s *Repository) insertLocked(job *model.Job, now time.Time) {
	job.State, job.CreatedAt, job.QueuedAt = model.StatePending, now, now
	s.jobs[job.ID] = job
	s.indexChildLocked(*job)
	s.appendOrderLocked(job.ID)
}

// artifactCopyLocked returns a copy of the stored artifact, or nil when the job
// has none. The caller holds s.mu.
func (s *Repository) artifactCopyLocked(id string) *model.Artifact {
	artifact, ok := s.artifacts[id]
	if !ok {
		return nil
	}
	return &artifact
}

// jobCopyWithArtifactLocked returns a caller-owned copy of job with its
// artifact attached. Reads return copies so a caller cannot mutate queue state
// through a pointer. The caller holds s.mu.
func (s *Repository) jobCopyWithArtifactLocked(job *model.Job) *model.Job {
	copy := *job
	copy.Artifact = s.artifactCopyLocked(job.ID)
	return &copy
}

// jobsByParentLocked materializes the parent's indexed jobs as caller-owned
// copies. Children and ByParent differ only in the ordering they apply. The
// caller holds s.mu.
func (s *Repository) jobsByParentLocked(parentJobID string) []*model.Job {
	ids := s.children[parentJobID]
	jobs := make([]*model.Job, 0, len(ids))
	for id := range ids {
		if job := s.jobs[id]; job != nil {
			jobs = append(jobs, s.jobCopyWithArtifactLocked(job))
		}
	}
	return jobs
}

// claimCandidateLocked claims id when it is still claimable. The snapshot pass
// and the concurrent-tail pass in ClaimState must apply identical rules, so the
// predicate lives here once. The caller holds s.mu for writing.
func (s *Repository) claimCandidateLocked(id, workerID string, state model.State, now time.Time) (*model.Job, bool) {
	job := s.jobs[id]
	if job == nil || (state != "" && job.State != state) ||
		(state == "" && !model.IsClaimable(job.State)) {
		return nil, false
	}
	if job.NotBefore != nil && job.NotBefore.After(now) {
		return nil, false
	}
	if s.hasChildrenLocked(id) {
		return nil, false
	}
	s.removeOrderLocked(id)
	job.State = model.StateRunning
	job.Worker = workerID
	job.Attempts++
	job.StartedAt = now
	job.LeaseUntil = job.StartedAt.Add(s.lease)
	job.Artifact = s.artifactCopyLocked(id)
	return job, true
}

// indexChildLocked records job as a child of its declared parent. The caller
// holds s.mu for writing. It is the in-memory mirror of the postgres child
// lookup (children.go), and the single place the children index is grown: every
// insert path must call it or the claim's anchor rule silently stops protecting
// a family.
func (s *Repository) indexChildLocked(job model.Job) {
	if job.ParentJobID == "" {
		return
	}
	set := s.children[job.ParentJobID]
	if set == nil {
		set = make(map[string]struct{})
		s.children[job.ParentJobID] = set
	}
	set[job.ID] = struct{}{}
}

func (s *Repository) SubmitIdempotent(_ context.Context, job model.Job) (*model.Job, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if job.ID == "" {
		return nil, false, fmt.Errorf("job id is required")
	}
	// O(1) idempotency lookup through a dedicated index instead of scanning
	// the whole job map per submit (mirrors the postgres unique index).
	if job.IdempotencyKey != "" {
		if id, ok := s.idem[job.IdempotencyKey]; ok {
			if existing, exists := s.jobs[id]; exists {
				copy := *existing
				return &copy, false, nil
			}
		}
	}
	if _, exists := s.jobs[job.ID]; exists {
		return nil, false, fmt.Errorf("%w: job %s", repository.ErrJobExists, job.ID)
	}
	now := time.Now()
	if err := model.ValidateChunk(job); err != nil {
		return nil, false, err
	}
	s.insertLocked(&job, now)
	if job.IdempotencyKey != "" {
		s.idem[job.IdempotencyKey] = job.ID
	}
	copy := job
	return &copy, true, nil
}

// Submit enqueues a job. The ID is required and must be unique.
func (s *Repository) Submit(_ context.Context, job model.Job) error {
	if job.ID == "" {
		return fmt.Errorf("job id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.jobs[job.ID]; exists {
		return fmt.Errorf("%w: job %s", repository.ErrJobExists, job.ID)
	}
	now := time.Now()
	if err := model.ValidateChunk(job); err != nil {
		return err
	}
	s.insertLocked(&job, now)
	return nil
}

// SubmitBatch inserts an entire anchor+children family while holding the
// repository lock. Validation happens before mutation, so a malformed child
// or duplicate never leaves a partial family behind.
func (s *Repository) SubmitBatch(_ context.Context, jobs []model.Job) error {
	if err := model.ValidateJobBatch(jobs); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, job := range jobs {
		if _, ok := s.jobs[job.ID]; ok {
			return fmt.Errorf("%w: job %s", repository.ErrJobExists, job.ID)
		}
	}
	now := time.Now()
	for _, job := range jobs {
		copy := job
		s.insertLocked(&copy, now)
		if job.IdempotencyKey != "" {
			s.idem[job.IdempotencyKey] = job.ID
		}
	}
	return nil
}

// RecoverableParents returns parent IDs with child jobs that can be adopted or retried.
func (s *Repository) RecoverableParents(_ context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0)
	for id, children := range s.children {
		job := s.jobs[id]
		if len(children) > 0 && job != nil && (job.State == model.StatePending || job.State == model.StateFinalizing) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// Claim atomically claims the oldest pending job for a worker and returns it
// with its lease duration. It returns nil when the queue is empty.
func (s *Repository) Claim(ctx context.Context, workerID string) (*model.Job, time.Duration, error) {
	return s.ClaimState(ctx, workerID, "")
}

func (s *Repository) ClaimState(_ context.Context, workerID string, state model.State) (*model.Job, time.Duration, error) {
	// Phase 1 (shared lock): snapshot the FIFO candidate IDs in order.
	// Preallocate to the queue length so the snapshot is a single alloc.
	// Job rows are NOT copied here beyond their IDs: the predicate is
	// re-validated under the write lock (CAS), so a concurrent transition
	// between snapshot and claim cannot hand out a stale job.
	s.mu.RLock()
	candidates := make([]string, 0, s.order.Len())
	for el := s.order.Front(); el != nil; el = el.Next() {
		if id, ok := el.Value.(string); ok {
			candidates = append(candidates, id)
		}
	}
	s.mu.RUnlock()

	now := time.Now()
	// Phase 2 (exclusive lock, short): CAS the first still-claimable
	// candidate in FIFO order. FIFO + the assembly-anchor rule are enforced
	// here on live state, so the snapshot ordering is the claim ordering.
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range candidates {
		// Skip entries already unlinked by a concurrent claim (stale snapshot).
		if _, ok := s.orderIdx[id]; !ok {
			continue
		}
		if job, ok := s.claimCandidateLocked(id, workerID, state, now); ok {
			return job, s.lease, nil
		}
	}
	// Snapshot missed a job submitted concurrently between RUnlock and Lock
	// (tail growth). One live scan under the already-held write lock closes
	// the race without another lock round-trip; it runs only when the
	// snapshot found nothing claimable.
	for el := s.order.Front(); el != nil; el = el.Next() {
		id, ok := el.Value.(string)
		if !ok {
			continue
		}
		claimed := false
		for _, c := range candidates {
			if c == id {
				claimed = true
				break
			}
		}
		if claimed {
			continue
		}
		if job, ok := s.claimCandidateLocked(id, workerID, state, now); ok {
			return job, s.lease, nil
		}
	}
	return nil, 0, nil
}

// hasChildrenLocked reports whether another job declares this id as its parent.
// The caller holds s.mu (ClaimState does). It is the in-memory encoding of the
// assembly-anchor rule in model/lifecycle.go and lives next to the claim it
// guards, so the rule and the claim cannot be edited apart.
//
// It reads the children index, so a claim walks its pending candidates in O(1)
// each instead of scanning every job row per candidate.
func (s *Repository) hasChildrenLocked(id string) bool {
	return len(s.children[id]) > 0
}

// Get returns the current state of a job, including its artifact when done.
func (s *Repository) Get(_ context.Context, id string) (*model.Job, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	job := s.jobs[id]
	if job == nil {
		return nil, fmt.Errorf("job %s: %w", id, repository.ErrNotFound)
	}
	return s.jobCopyWithArtifactLocked(job), nil
}

// Children returns child chunks in deterministic chunk order.
//
// It walks the parent's entry in the children index rather than every job, so a
// family read costs its own size instead of the whole queue's.
func (s *Repository) Children(_ context.Context, parentJobID string) ([]*model.Job, error) {
	if parentJobID == "" {
		return nil, fmt.Errorf("parent job id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	children := s.jobsByParentLocked(parentJobID)
	sort.Slice(children, func(i, j int) bool {
		return children[i].ChunkIndex < children[j].ChunkIndex
	})
	return children, nil
}

// ByParent returns every job submitted under one parent_job_id, ordered by
// queued_at then id so the read is deterministic for equal timestamps.
//
// It walks the parent index rather than every job, so the read costs its own
// size (the same reason Children uses it) and an unknown parent is an empty
// answer, not an error.
func (s *Repository) ByParent(_ context.Context, parentJobID string) ([]*model.Job, error) {
	if parentJobID == "" {
		return nil, fmt.Errorf("parent job id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	jobs := s.jobsByParentLocked(parentJobID)
	sort.Slice(jobs, func(i, j int) bool {
		if !jobs[i].QueuedAt.Equal(jobs[j].QueuedAt) {
			return jobs[i].QueuedAt.Before(jobs[j].QueuedAt)
		}
		return jobs[i].ID < jobs[j].ID
	})
	return jobs, nil
}

// ClaimFinalization atomically claims a parent row for one finalizer.
func (s *Repository) ClaimFinalization(_ context.Context, parentJobID, workerID string) (*model.Job, bool, error) {
	if parentJobID == "" || workerID == "" {
		return nil, false, fmt.Errorf("parent job id and worker id are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.jobs[parentJobID]
	if job == nil {
		return nil, false, fmt.Errorf("parent job %s: %w", parentJobID, repository.ErrNotFound)
	}
	if job.State == model.StateFinalizing {
		return nil, false, nil
	}
	if job.State != model.StatePending && job.State != model.StateRunning {
		return nil, false, fmt.Errorf("parent job %s is in state %q", parentJobID, job.State)
	}
	job.State = model.StateFinalizing
	job.Worker = workerID
	job.LeaseUntil = time.Now().Add(s.lease)
	copy := *job
	return &copy, true, nil
}

// Complete marks a running job as completed and records its artifact.
func (s *Repository) Complete(_ context.Context, id, workerID string, artifact model.Artifact) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, err := s.runningJob(id, workerID)
	if err != nil {
		return err
	}
	job.State = model.StateCompleted
	job.CompletedAt = time.Now()
	s.artifacts[id] = artifact
	return nil
}

// Rendered marks a running job as rendered: its artifact is durably stored, but
// external publication failed, so the job stays out of `completed` and is
// re-claimable for a publication-only retry.
func (s *Repository) Rendered(_ context.Context, id, workerID string, artifact model.Artifact, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, err := s.runningJob(id, workerID)
	if err != nil {
		return err
	}
	job.State = model.StateRendered
	job.FailReason = reason
	job.Worker = ""
	job.QueuedAt = time.Now()
	// Mirror PostgreSQL: a rendered job holds no lease — it is only claimable
	// again through an explicit (re)claim, never through lease expiry.
	job.LeaseUntil = time.Time{}
	s.artifacts[id] = artifact
	s.appendOrderLocked(id)
	return nil
}

// Fail marks a running job failed. Jobs that have not exhausted their attempts
// are requeued; otherwise they are permanently failed.
func (s *Repository) Fail(_ context.Context, id, workerID, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, err := s.runningJob(id, workerID)
	if err != nil {
		return err
	}
	job.FailReason = reason
	if model.ShouldFailPermanently(job.Attempts, s.maxAttempts) {
		job.State = model.StateFailed
		return nil
	}
	job.State = model.StatePending
	job.Worker = ""
	job.QueuedAt = time.Now()
	// Mirror PostgreSQL: requeued jobs hold no lease until the next claim.
	job.LeaseUntil = time.Time{}
	s.appendOrderLocked(id)
	return nil
}

// FailPermanently marks a running job terminally failed, regardless of the
// normal max-attempt retry budget. It is reserved for non-retryable failures.
func (s *Repository) FailPermanently(_ context.Context, id, workerID, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, err := s.runningJob(id, workerID)
	if err != nil {
		return err
	}
	job.State = model.StateFailed
	job.FailReason = reason
	job.Worker = ""
	job.LeaseUntil = time.Time{}
	return nil
}

// Renew extends the lease for a running job owned by workerID.
func (s *Repository) Renew(_ context.Context, id, workerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, err := s.runningJob(id, workerID)
	if err != nil {
		return err
	}
	job.LeaseUntil = time.Now().Add(s.lease)
	return nil
}

// Retry resets a failed job back to pending state for re-execution.
func (s *Repository) Retry(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, exists := s.jobs[id]
	if !exists {
		return fmt.Errorf("job %s: %w", id, repository.ErrNotFound)
	}
	if job.State != model.StateFailed {
		return fmt.Errorf("job %s is in state %q, cannot retry non-failed job", id, job.State)
	}
	job.State = model.StatePending
	job.Worker = ""
	job.FailReason = ""
	job.QueuedAt = time.Now()
	job.LeaseUntil = time.Time{}
	job.Progress = nil
	s.appendOrderLocked(id)
	return nil
}

// Cancel moves a job that has not reached a terminal state to cancelled.
// Cancelled is terminal: ClaimState skips it (it is no longer pending or
// rendered) and RequeueExpired only touches running/finalizing jobs, so a
// cancelled job is never claimed again and never requeued after its lease
// elapses. Cancelling an already-cancelled job is a no-op; cancelling a
// completed/failed job is an error. Attempts are deliberately NOT incremented:
// every claim that would have invoked Chronon already happened.
func (s *Repository) Cancel(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, exists := s.jobs[id]
	if !exists {
		return fmt.Errorf("job %s: %w", id, repository.ErrNotFound)
	}
	if job.State == model.StateCancelled {
		// Idempotent: the producer already cancelled this job.
		return nil
	}
	if !model.IsCancelable(job.State) {
		return fmt.Errorf("job %s is in state %q, cannot cancel terminal job", id, job.State)
	}
	job.State = model.StateCancelled
	job.Worker = ""
	job.LeaseUntil = time.Time{}
	job.Progress = nil
	return nil
}

// SetProgress stores the latest render progress from the lease-owning worker.
// It rejects reports for jobs that are not running or whose lease moved to
// another worker, mirroring the PostgreSQL backend.
func (s *Repository) SetProgress(_ context.Context, id, workerID string, p model.Progress) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, err := s.runningJob(id, workerID)
	if err != nil {
		return err
	}
	copy := p
	copy.Worker = workerID
	copy.LastFrameAt = time.Now()
	job.Progress = &copy
	return nil
}

// RequeueExpired moves running jobs whose lease has elapsed back to pending,
// or permanently fails them if they exhausted their attempts. It returns the
// number of jobs affected.
func (s *Repository) RequeueExpired(_ context.Context, now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	n := 0
	for _, job := range s.jobs {
		if (job.State != model.StateRunning && job.State != model.StateFinalizing) || job.LeaseUntil.IsZero() || !now.After(job.LeaseUntil) {
			continue
		}
		if model.ShouldFailPermanently(job.Attempts, s.maxAttempts) {
			job.State = model.StateFailed
			job.FailReason = "lease expired, max attempts reached"
		} else {
			job.State = model.StatePending
			job.Worker = ""
			job.QueuedAt = time.Now()
			s.appendOrderLocked(job.ID)
		}
		n++
	}
	return n, nil
}

// Stats returns a snapshot of the queue state. It takes the lock shared, so the
// pending gauge read (throttled to once per interval, but on the same service
// as every state transition) cannot serialize a claim.
func (s *Repository) Stats(_ context.Context) model.Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := model.Stats{}
	var oldestPending time.Time
	sawPending := false
	for _, job := range s.jobs {
		switch job.State {
		case model.StatePending:
			stats.Pending++
			if !sawPending || job.QueuedAt.Before(oldestPending) {
				oldestPending = job.QueuedAt
				sawPending = true
			}
		case model.StateRunning:
			stats.Running++
		case model.StateCompleted:
			stats.Completed++
		case model.StateFailed:
			stats.Failed++
		}
	}
	// Depth counts claimable render work only, matching PostgreSQL (where the
	// gauge reads state='pending'). Rendered-state jobs are claimable too but
	// are not render work; the gauge exists for autoscaling render capacity.
	stats.Ok = true
	stats.Depth = stats.Pending
	if sawPending && !oldestPending.IsZero() {
		stats.OldestPendingSeconds = time.Since(oldestPending).Seconds()
	}
	return stats
}

// runningJob returns the job if it is running and owned by workerID.
func (s *Repository) runningJob(id, workerID string) (*model.Job, error) {
	job := s.jobs[id]
	if job == nil {
		return nil, fmt.Errorf("job %s not found", id)
	}
	if job.State != model.StateRunning && job.State != model.StateFinalizing {
		return nil, fmt.Errorf("job %s is not running or finalizing", id)
	}
	if job.Worker != workerID {
		return nil, fmt.Errorf("job %s is owned by %s, not %s", id, job.Worker, workerID)
	}
	return job, nil
}
