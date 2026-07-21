// Package mediamemory — batch_service.go is the canonical SSOT for
// the 1000-candidate catalog-only batch surface (architecture doc
// section 8).
//
// godlike/06 SSOT: BatchService is the SINGLE owner of the parent/
// child batch model. Every catalog-only run, every reconcile
// request, every resume-and-recover call routes through this
// service. The parent row is the canonical record of policy
// (MaxCandidates, MaterializeTopK, Mode); children carry per-
// (query × provider) execution state.
//
// godlike/06 SSOT (resume contract): children are referenced by
// ID; on resume the worker re-reads the parent's Spec to recover
// the canonical policy. Direct child writes outside BatchService
// are forward-prevention forbidden.
//
// godlike/07 NO-FAKE-AVAILABILITY: terminal batches (State ==
// Completed/Failed) refuse new Candidates via ErrBatchNotReconcilable.
// Progress numbers are computed from the durable child state
// (not from in-memory progress vars) so a worker crash + restart
// produces the same numbers.
package mediamemory

import "context"

// BatchService is the canonical port. Concrete impl is
// defaultBatchService below.
type BatchService interface {
	// CreateBatch validates the input BatchSpec and produces a
	// parent Batch row + N empty BatchChild rows (one per
	// query × provider combination).
	CreateBatch(ctx context.Context, spec BatchSpec) (Batch, []BatchChild, error)

	// AppendCandidate adds a candidate to the batch child. Used
	// by the discovery worker. Surface terminal-state refusal via
	// wrapped ErrBatchNotReconcilable.
	AppendCandidate(ctx context.Context, childID string, candidate MediaCandidate) error

	// MarkMaterialized bumps the parent's MaterializedCount and
	// the child's terminal status when rights flow through.
	MarkMaterialized(ctx context.Context, childID string, candidateID string, tier MaterializationStatus) error

	// Reconcile finalises the batch (State = Completed or Failed)
	// and computes CandidateCount, IndexedCount, MaterializedCount
	// from durable state.
	Reconcile(ctx context.Context, batchID string) (Batch, error)

	// Get returns the canonical parent row by id.
	Get(ctx context.Context, batchID string) (Batch, error)

	// Resume returns an in-flight batch's children that still
	// need candidates appended. Used by worker crash recovery.
	Resume(ctx context.Context, batchID string) ([]BatchChild, error)
}

// ── Default implementation (skeleton) ─────────────────────────────

// defaultBatchService is the canonical implementation.
type defaultBatchService struct {
	candidates CandidateRepository
	planner    AcquisitionPlanner
	rights     RightsValidator
	log        Logger
	clock      Clock
}

// NewDefaultBatchService constructs the service.
func NewDefaultBatchService(
	candidates CandidateRepository,
	planner AcquisitionPlanner,
	rights RightsValidator,
	log Logger,
	clock Clock,
) *defaultBatchService {
	if log == nil {
		log = NoopLogger()
	}
	if clock == nil {
		clock = RealClock()
	}
	return &defaultBatchService{
		candidates: candidates,
		planner:    planner,
		rights:     rights,
		log:        log,
		clock:      clock,
	}
}

var _ BatchService = (*defaultBatchService)(nil)

// CreateBatch is the canonical Phase 1.x entrypoint: identity
// stub; Phase 3 wires the (validate → fan-out → persist parent
// + N children) chain.
func (s *defaultBatchService) CreateBatch(_ context.Context, _ BatchSpec) (Batch, []BatchChild, error) {
	return Batch{}, nil, errNotImplemented("mediamemory: defaultBatchService.CreateBatch not yet implemented (Phase 3)")
}

func (s *defaultBatchService) AppendCandidate(_ context.Context, _ string, _ MediaCandidate) error {
	return errNotImplemented("mediamemory: defaultBatchService.AppendCandidate not yet implemented (Phase 3)")
}

func (s *defaultBatchService) MarkMaterialized(_ context.Context, _ string, _ string, _ MaterializationStatus) error {
	return errNotImplemented("mediamemory: defaultBatchService.MarkMaterialized not yet implemented (Phase 3)")
}

func (s *defaultBatchService) Reconcile(_ context.Context, _ string) (Batch, error) {
	return Batch{}, errNotImplemented("mediamemory: defaultBatchService.Reconcile not yet implemented (Phase 3)")
}

func (s *defaultBatchService) Get(_ context.Context, _ string) (Batch, error) {
	return Batch{}, errNotImplemented("mediamemory: defaultBatchService.Get not yet implemented (Phase 3)")
}

func (s *defaultBatchService) Resume(_ context.Context, _ string) ([]BatchChild, error) {
	return nil, errNotImplemented("mediamemory: defaultBatchService.Resume not yet implemented (Phase 3)")
}
