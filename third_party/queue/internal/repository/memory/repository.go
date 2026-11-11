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
	"encoding/json"
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
	_ repository.BatchIdempotencyRepository = (*Repository)(nil)
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
	// Store a deep copy: the caller still owns the slices/pointers it passed
	// in and may mutate them after Submit returns.
	stored := cloneJob(job)
	stored.State, stored.CreatedAt, stored.QueuedAt = model.StatePending, now, now
	s.jobs[stored.ID] = stored
	s.indexChildLocked(*stored)
	s.appendOrderLocked(stored.ID)
}

// cloneFloatMap copies an artifact metrics map. nil stays nil so a copy of an
// artifact without metrics does not grow an empty map.
func cloneFloatMap(m map[string]float64) map[string]float64 {
	if m == nil {
		return nil
	}
	copy := make(map[string]float64, len(m))
	for k, v := range m {
		copy[k] = v
	}
	return copy
}

// cloneOutputFacts deep-copies the nested certification, including the
// per-segment batch facts, so a consumer mutating the returned document cannot
// alter the artifact ledger.
func cloneOutputFacts(facts *model.OutputFacts) *model.OutputFacts {
	if facts == nil {
		return nil
	}
	copy := *facts
	// Re-slice to a zero-capacity slice so append allocates an independent
	// backing array; the element type is the wire type and needs no local
	// re-declaration here.
	copy.BatchSegments = append(facts.BatchSegments[:0:0], facts.BatchSegments...)
	for i := range copy.BatchSegments {
		copy.BatchSegments[i].OutputFacts = cloneOutputFacts(facts.BatchSegments[i].OutputFacts)
	}
	return &copy
}

// cloneProvenance deep-copies the provenance record (its item-id slice and
// producer telemetry bytes).
func cloneProvenance(p *model.ArtifactProvenance) *model.ArtifactProvenance {
	if p == nil {
		return nil
	}
	copy := *p
	copy.ItemIDs = append([]string(nil), p.ItemIDs...)
	copy.ProducerTelemetry = append(json.RawMessage(nil), p.ProducerTelemetry...)
	return &copy
}

// cloneArtifact returns a deep copy of an artifact. The wire type carries a
// metrics map, telemetry bytes, nested output facts and provenance; a shallow
// struct copy would hand all of those to the caller.
func cloneArtifact(artifact *model.Artifact) *model.Artifact {
	if artifact == nil {
		return nil
	}
	copy := *artifact
	copy.Metrics = cloneFloatMap(artifact.Metrics)
	copy.ChrononTelemetry = append(json.RawMessage(nil), artifact.ChrononTelemetry...)
	copy.OutputFacts = cloneOutputFacts(artifact.OutputFacts)
	copy.Provenance = cloneProvenance(artifact.Provenance)
	return &copy
}

// cloneJob returns a deep copy of a job. Every reference field on the wire
// type (render plan, assets, frame range, deferral time, progress and the
// artifact graph) is duplicated, so a returned job never aliases queue state.
func cloneJob(job *model.Job) *model.Job {
	if job == nil {
		return nil
	}
	copy := *job
	copy.RenderPlan = append(json.RawMessage(nil), job.RenderPlan...)
	copy.Assets = append([]model.AssetRef(nil), job.Assets...)
	if job.PreparedRenderPackage != nil {
		preparedPackage := *job.PreparedRenderPackage
		copy.PreparedRenderPackage = &preparedPackage
	}
	if job.FrameRange != nil {
		frameRange := *job.FrameRange
		copy.FrameRange = &frameRange
	}
	if job.NotBefore != nil {
		notBefore := *job.NotBefore
		copy.NotBefore = &notBefore
	}
	if job.Progress != nil {
		progress := *job.Progress
		copy.Progress = &progress
	}
	copy.Artifact = cloneArtifact(job.Artifact)
	return &copy
}

// artifactCopyLocked returns a deep copy of the stored artifact, or nil when
// the job has none. The caller holds s.mu.
func (s *Repository) artifactCopyLocked(id string) *model.Artifact {
	artifact, ok := s.artifacts[id]
	if !ok {
		return nil
	}
	return cloneArtifact(&artifact)
}

// jobCopyWithArtifactLocked returns a caller-owned deep copy of job with its
// artifact attached. Reads return copies so a caller cannot mutate queue state
// through a pointer. The caller holds s.mu.
func (s *Repository) jobCopyWithArtifactLocked(job *model.Job) *model.Job {
	copy := cloneJob(job)
	copy.Artifact = s.artifactCopyLocked(job.ID)
	return copy
}

// idemKeyLocked returns the live job id registered for an idempotency key.
// The caller holds s.mu.
func (s *Repository) idemKeyLocked(key string) (string, bool) {
	if key == "" {
		return "", false
	}
	id, ok := s.idem[key]
	if !ok {
		return "", false
	}
	if _, exists := s.jobs[id]; !exists {
		return "", false
	}
	return id, true
}

// indexIdempotencyLocked records the key -> id mapping for an inserted job.
// The caller holds s.mu. Every insert path must call it: Submit used to skip
// the index entirely, which let a later submit reuse a key that PostgreSQL's
// unique index rejects.
func (s *Repository) indexIdempotencyLocked(job model.Job) {
	if job.IdempotencyKey == "" {
		return
	}
	s.idem[job.IdempotencyKey] = job.ID
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
	job.LeaseGeneration++
	job.StartedAt = now
	job.LeaseUntil = job.StartedAt.Add(s.lease)
	// Hand back a caller-owned snapshot, not the map's pointer: ClaimState
	// releases the write lock before the caller sees it, and an escaped
	// pointer would let a worker mutate queue state without the mutex.
	return s.jobCopyWithArtifactLocked(job), true
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
	if id, ok := s.idemKeyLocked(job.IdempotencyKey); ok {
		canonical := s.jobs[id]
		if !model.SameSubmitRequest(*canonical, job) {
			return nil, false, fmt.Errorf("%w: key %q belongs to job %s", repository.ErrIdempotencyConflict, job.IdempotencyKey, id)
		}
		return s.jobCopyWithArtifactLocked(canonical), false, nil
	}
	if _, exists := s.jobs[job.ID]; exists {
		return nil, false, fmt.Errorf("%w: job %s", repository.ErrJobExists, job.ID)
	}
	now := time.Now()
	if err := model.ValidateChunk(job); err != nil {
		return nil, false, err
	}
	s.insertLocked(&job, now)
	s.indexIdempotencyLocked(job)
	// Read the stored row back: insertLocked stores a clone and stamps its
	// state/timestamps there, so the local input is not the canonical copy.
	return s.jobCopyWithArtifactLocked(s.jobs[job.ID]), true, nil
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
	// Mirror the postgres unique index: a non-empty key identifies ONE
	// logical job, so reusing it is a conflict even outside SubmitIdempotent.
	if id, ok := s.idemKeyLocked(job.IdempotencyKey); ok {
		return fmt.Errorf("%w: idempotency key %q is already used by job %s", repository.ErrJobExists, job.IdempotencyKey, id)
	}
	now := time.Now()
	if err := model.ValidateChunk(job); err != nil {
		return err
	}
	s.insertLocked(&job, now)
	s.indexIdempotencyLocked(job)
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
	// Idempotency keys are checked against the whole store BEFORE the first
	// insert, so a conflicting batch leaves no partial family behind — the
	// same all-or-nothing outcome as the PostgreSQL transaction rolling back
	// on its unique index.
	for _, job := range jobs {
		if id, ok := s.idemKeyLocked(job.IdempotencyKey); ok {
			return fmt.Errorf("%w: idempotency key %q is already used by job %s", repository.ErrJobExists, job.IdempotencyKey, id)
		}
	}
	now := time.Now()
	for _, job := range jobs {
		s.insertLocked(&job, now)
		s.indexIdempotencyLocked(job)
	}
	return nil
}

// SubmitBatchIdempotent creates a family under the write lock or accepts an
// exact complete replay. Partial families and changed members fail closed.
func (s *Repository) SubmitBatchIdempotent(_ context.Context, jobs []model.Job) (bool, error) {
	if err := model.ValidateJobBatch(jobs); err != nil {
		return false, err
	}
	for _, job := range jobs {
		if job.IdempotencyKey == "" {
			return false, fmt.Errorf("job %s has no idempotency key", job.ID)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	existingCount := 0
	for _, requested := range jobs {
		byID := s.jobs[requested.ID]
		keyID, hasKey := s.idemKeyLocked(requested.IdempotencyKey)
		if byID == nil && !hasKey {
			continue
		}
		if byID == nil || !hasKey || keyID != requested.ID || !model.SameSubmitRequest(*byID, requested) {
			return false, fmt.Errorf("%w: batch member %s differs from the stored request", repository.ErrIdempotencyConflict, requested.ID)
		}
		existingCount++
	}
	if existingCount == len(jobs) {
		return false, nil
	}
	if existingCount != 0 {
		return false, fmt.Errorf("%w: only %d of %d batch members exist", repository.ErrIdempotencyConflict, existingCount, len(jobs))
	}
	now := time.Now()
	for _, job := range jobs {
		s.insertLocked(&job, now)
		s.indexIdempotencyLocked(job)
	}
	return true, nil
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
	seen := make(map[string]struct{}, s.order.Len())
	for el := s.order.Front(); el != nil; el = el.Next() {
		if id, ok := el.Value.(string); ok {
			candidates = append(candidates, id)
			seen[id] = struct{}{}
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
	// snapshot found nothing claimable. `seen` is a set, not a linear lookup:
	// a queue full of deferred or anchor jobs used to make this fallback
	// O(pending^2), once per empty claim.
	for el := s.order.Front(); el != nil; el = el.Next() {
		id, ok := el.Value.(string)
		if !ok {
			continue
		}
		if _, inSnapshot := seen[id]; inSnapshot {
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
	// Mirror the PostgreSQL invariant: only a job that OWNS assembable chunk
	// children may be claimed by the finalizer. Without this, the transition
	// below rewrites Worker and LeaseUntil on an ordinary running job and steals
	// it from the render worker that still holds it.
	if !s.hasChildrenLocked(parentJobID) {
		return nil, false, fmt.Errorf("job %s has no chunk children: only an assembly anchor may be finalized", parentJobID)
	}
	if job.State == model.StateFinalizing {
		return nil, false, nil
	}
	if job.State != model.StatePending && job.State != model.StateRunning {
		return nil, false, fmt.Errorf("parent job %s is in state %q", parentJobID, job.State)
	}
	job.State = model.StateFinalizing
	job.LeaseGeneration++
	job.Worker = workerID
	job.LeaseUntil = time.Now().Add(s.lease)
	return s.jobCopyWithArtifactLocked(job), true, nil
}

// Complete marks a running job as completed and records its artifact.
func (s *Repository) Complete(_ context.Context, id, workerID string, generation int64, artifact model.Artifact) error {
	return s.complete(id, workerID, generation, artifact)
}

func (s *Repository) complete(id, workerID string, generation int64, artifact model.Artifact) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, err := s.runningJob(id, workerID, generation)
	if err != nil {
		return err
	}
	job.State = model.StateCompleted
	job.CompletedAt = time.Now()
	s.artifacts[id] = *cloneArtifact(&artifact)
	return nil
}

// Rendered marks a running job as rendered: its artifact is durably stored, but
// external publication failed, so the job stays out of `completed` and is
// re-claimable for a publication-only retry.
func (s *Repository) Rendered(_ context.Context, id, workerID string, generation int64, artifact model.Artifact, reason string) error {
	return s.rendered(id, workerID, generation, artifact, reason)
}

func (s *Repository) rendered(id, workerID string, generation int64, artifact model.Artifact, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, err := s.runningJob(id, workerID, generation)
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
	s.artifacts[id] = *cloneArtifact(&artifact)
	s.appendOrderLocked(id)
	return nil
}

// Fail marks a running job failed. Jobs that have not exhausted their attempts
// are requeued; otherwise they are permanently failed.
func (s *Repository) Fail(_ context.Context, id, workerID string, generation int64, reason string) error {
	return s.fail(id, workerID, generation, reason)
}

func (s *Repository) fail(id, workerID string, generation int64, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, err := s.runningJob(id, workerID, generation)
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
} // FailPermanently marks a running job terminally failed, regardless of the
// normal max-attempt retry budget. It is reserved for non-retryable failures.
func (s *Repository) FailPermanently(_ context.Context, id, workerID string, generation int64, reason string) error {
	return s.failPermanently(id, workerID, generation, reason)
}

func (s *Repository) failPermanently(id, workerID string, generation int64, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, err := s.runningJob(id, workerID, generation)
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
func (s *Repository) Renew(_ context.Context, id, workerID string, generation int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, err := s.runningJob(id, workerID, generation)
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
func (s *Repository) SetProgress(_ context.Context, id, workerID string, generation int64, p model.Progress) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, err := s.runningJob(id, workerID, generation)
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

// runningJob returns the job if it is running, owned by workerID and still
// on the claim generation. The worker id alone cannot distinguish two attempts
// of the same worker after a lease expiry.
func (s *Repository) runningJob(id, workerID string, generation int64) (*model.Job, error) {
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
	if job.LeaseGeneration != generation {
		return nil, fmt.Errorf("job %s lease generation %d is stale (current %d)", id, generation, job.LeaseGeneration)
	}
	return job, nil
}
