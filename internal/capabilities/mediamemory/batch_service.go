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
// godlike/06 SSOT (Fase 3.1 minimal viable): this implementation
// tracks Batch + BatchChild rows in memory under two maps guarded
// by a mutex. Durability (SQL migrations + repos for media_batches
// + media_batch_children) lands in Fase 3.4 along with the
// resume-after-worker-crash flow. The interface surface is
// already durable-shaped — Fase 3.4 swaps the in-memory store
// for the canonical sqlite-backed repository WITHOUT changing the
// BatchService port signature.
//
// godlike/07 NO-FAKE-AVAILABILITY: terminal batches (State ==
// Completed/Failed) refuse new Candidates via ErrBatchNotReconcilable
// before the worker keeps appending. The map-state checks happen
// BEFORE the in-memory write so a roll-forward race cannot produce
// a partial append.
//
// File split (godlike/06 single canonical home per responsibility):
//   - batch_service.go                : BatchService port + struct + ctors + lifecycle wiring  ← this file
//   - batch_service_validation.go     : validateSpec + specsStructurallyEqual + isTerminalState
//   - batch_service_persistence.go    : CreateBatch/AppendCandidate/MarkMaterialized/internal reads
//   - batch_service_lifecycle.go      : Get/Resume/Reconcile (terminal-state machine)
//   - batch_service_orchestrator.go   : RunCatalogOnly/EnrichLinker/loadChildCandidates
//   - batch_materialization.go        : MaterializeTopK/PromoteOnDemand/recordParentFailure (Fase 3.3)
package mediamemory

import (
	"context"
	"fmt"
	"sync"
	"time"
)

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

	// RunCatalogOnly is the canonical Fase 3.1 orchestrator:
	// drives (query × provider) fan-out via DiscoveryWorker and
	// AppendCandidate for each persisted candidate, then calls
	// Reconcile. godlike/06 SSOT: every catalog_only batch in the
	// system MUST route through this entrypoint so the parent/
	// child model and the rights-gating live in ONE place.
	//
	// Returns the final parent Batch row + the children produced.
	// Failures are recorded against the parent Failures[] field so
	// the dashboard can surface per-child rationale.
	RunCatalogOnly(ctx context.Context, spec BatchSpec) (Batch, []BatchChild, error)

	// MaterializeTopK (Fase 3.3): drives the canonical Cold→Warm
	// (or Warm→Hot when HotCache=true) promotion pass for the
	// MaterializeTopK candidates of a finalizeable batch. The
	// orchestrator dispatches AcquisitionPlanner.Plan to select
	// the canonical Top-K by CandidateScore desc, then forwards
	// the resulting AcquisitionPromote set to the wired
	// MaterializeWorker. Per-candidate failures land on the
	// parent's Failures[] surface; transient failures leave the
	// candidate's MaterializationStatus unchanged so a subsequent
	// call resumes deterministically.
	//
	// godlike/06 SSOT: terminal-state (BatchFailed /
	// BatchCompleted) batches refuse with wrapped
	// ErrBatchNotReconcilable — compose Reconcile explicitly
	// when the operator wants to commit.
	MaterializeTopK(ctx context.Context, batchID string) (Batch, error)

	// PromoteOnDemand (Fase 3.3): single-candidate Warm→Hot
	// promotion invoked by the resolver / scene-render pipeline
	// when a candidate is selected for a video clip. Returns
	// the canonical updated MediaCandidate with AssetID populated
	// and Discovery=DiscoveryMaterialized /
	// Materialization=MaterializationHot. Rights-denied candidates
	// surface as wrapped ErrApprovalRequired.
	PromoteOnDemand(ctx context.Context, candidate MediaCandidate, opts MaterializeOptions) (MediaCandidate, error)
}

// ── Default implementation ─────────────────────────────────

// batchRow is the in-memory parent row (godlike/06 SSOT:
// mirror of Batch entity with one extra mutex-guarded map).
type batchRow struct {
	batch   Batch
	mu      sync.Mutex
	created time.Time
}

// batchChildRow is the in-memory per-(query × provider) child.
type batchChildRow struct {
	child      BatchChild
	mu         sync.Mutex
	failures   []string
	persistedN int
	created    time.Time
}

// defaultBatchService is the canonical implementation.
type defaultBatchService struct {
	candidates        CandidateRepository
	planner           AcquisitionPlanner
	rights            RightsValidator
	external          SearchFanOut
	worker            DiscoveryWorker
	linker            LinkerWorker      // optional — wired by composition root via SetLinker for Fase 3.2
	materializeWorker MaterializeWorker // optional — wired by composition root via SetMaterializeWorker for Fase 3.3
	log               Logger
	clock             Clock

	// In-memory minimal viable store (Fase 3.4 swap point).
	mu       sync.RWMutex
	batches  map[string]*batchRow
	children map[string]*batchChildRow
}

// NewDefaultBatchService constructs the service without a
// DiscoveryWorker. Use NewDefaultBatchServiceWithWorker when
// wiring the canonical catalog_only orchestrator.
func NewDefaultBatchService(
	candidates CandidateRepository,
	planner AcquisitionPlanner,
	rights RightsValidator,
	log Logger,
	clock Clock,
) *defaultBatchService {
	return NewDefaultBatchServiceWithWorker(candidates, planner, rights, nil, nil, log, clock)
}

// NewDefaultBatchServiceWithWorker is the canonical Fase 3.1
// constructor. Composition root uses this form so catalog_only
// routes through DiscoveryWorker.
func NewDefaultBatchServiceWithWorker(
	candidates CandidateRepository,
	planner AcquisitionPlanner,
	rights RightsValidator,
	external SearchFanOut,
	worker DiscoveryWorker,
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
		external:   external,
		worker:     worker,
		log:        log,
		clock:      clock,
		batches:    make(map[string]*batchRow),
		children:   make(map[string]*batchChildRow),
	}
}

var _ BatchService = (*defaultBatchService)(nil)

// SetLinker wires the canonical Fase 3.2 LinkerWorker into the
// BatchService. godlike/06 SSOT (composition pattern): the
// service is constructed without a linker; composition root
// calls SetLinker after wiring the canonical linker deps.
// godlike/07 NO-FAKE-AVAILABILITY: a nil argument is rejected
// so callers cannot accidentally zero-out a previously-wired
// linker (operator visibility).
func (s *defaultBatchService) SetLinker(linker LinkerWorker) error {
	if linker == nil {
		return fmt.Errorf("mediamemory: SetLinker refuses nil linker: %w", ErrInvalidPhrase)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.linker != nil {
		return fmt.Errorf("mediamemory: SetLinker refuses rewire (existing linker already wired): %w", ErrLinkerInvariantBroken)
	}
	s.linker = linker
	return nil
}

// HasLinker reports whether a LinkerWorker has been wired. godlike/06
// SSOT (visible state surface): the dashboard / health-check
// path uses this to surface whether enrich-linker is enabled.
func (s *defaultBatchService) HasLinker() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.linker != nil
}

// SetMaterializeWorker wires the canonical Fase 3.3
// MaterializeWorker into the BatchService. godlike/06 SSOT
// (composition pattern): the service is constructed without a
// materialize worker; composition root calls SetMaterializeWorker
// after wiring the canonical materialize + stockpipeline adapters.
//
// godlike/07 NO-FAKE-AVAILABILITY: a nil argument is rejected
// so callers cannot accidentally zero-out a previously-wired
// worker. A second SetMaterializeWorker call also rejects so
// the canonical "wire-once" contract is preserved.
func (s *defaultBatchService) SetMaterializeWorker(mw MaterializeWorker) error {
	if mw == nil {
		return fmt.Errorf("mediamemory: SetMaterializeWorker refuses nil worker: %w", ErrInvalidPhrase)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.materializeWorker != nil {
		return fmt.Errorf("mediamemory: SetMaterializeWorker refuses rewire: %w", ErrLinkerInvariantBroken)
	}
	s.materializeWorker = mw
	return nil
}

// HasMaterializeWorker reports whether a MaterializeWorker is
// wired (dashboard / health-check visibility).
func (s *defaultBatchService) HasMaterializeWorker() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.materializeWorker != nil
}
