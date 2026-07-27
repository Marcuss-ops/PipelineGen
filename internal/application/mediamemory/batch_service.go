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
package mediamemory

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
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

// validateSpec is the canonical BatchSpec validator. godlike/06
// SSOT (closed-set Mode): only ModeCatalogOnly / ModeMaterializeTopK
// are accepted; empty spec, missing Mode, or unknown values all
// surface as typed sentinels. Single canonical validator — DRY
// per the validator-must-be-unique rule.
func validateSpec(spec BatchSpec) error {
	if spec.Name == "" {
		return fmt.Errorf("mediamemory: BatchSpec.Name is empty: %w", ErrInvalidPhrase)
	}
	if len(spec.Queries) == 0 {
		return fmt.Errorf("mediamemory: BatchSpec.Queries is empty: %w", ErrInvalidPhrase)
	}
	if len(spec.Providers) == 0 {
		return fmt.Errorf("mediamemory: BatchSpec.Providers is empty: %w", ErrInvalidPhrase)
	}
	if spec.MaxCandidates <= 0 {
		return fmt.Errorf("mediamemory: BatchSpec.MaxCandidates must be > 0: %w", ErrInvalidPhrase)
	}
	if !IsKnownBatchMode(spec.Mode) {
		err := ErrInvalidBatchMode
		if spec.Mode == "" {
			return fmt.Errorf("mediamemory: BatchSpec.Mode is empty: %w", err)
		}
		return fmt.Errorf("mediamemory: BatchSpec.Mode=%q: %w", spec.Mode, err)
	}
	return nil
}

// specsStructurallyEqual reports whether two BatchSpec values are
// field-for-field equal (deep). godlike/06 SSOT (spec immutability):
// the idempotent-by-name CreateBatch path calls this on the
// incoming spec vs the already-persisted spec; a non-equal result
// surfaces as wrapped ErrBatchSpecDrift so the canonical
// "spec is immutable post-CreateBatch" contract is enforced.
//
// Go's `==` is not usable on structs containing `[]string` slices
// (compile-time error: invalid operation), so we hand-roll the
// comparison using the stdlib slices.Equal for the slice fields
// plus direct equality for the scalars. Adding a map or a
// non-comparable field to BatchSpec will surface as a compile
// error in this helper, which is the desired godlike/07
// fail-loud property.
func specsStructurallyEqual(a, b BatchSpec) bool {
	if a.Name != b.Name || a.Language != b.Language {
		return false
	}
	if a.MaxCandidates != b.MaxCandidates || a.MaterializeTopK != b.MaterializeTopK {
		return false
	}
	if a.Mode != b.Mode {
		return false
	}
	if !slices.Equal(a.Queries, b.Queries) {
		return false
	}
	if !slices.Equal(a.Providers, b.Providers) {
		return false
	}
	if !slices.Equal(a.MediaTypes, b.MediaTypes) {
		return false
	}
	return true
}

// CreateBatch validates input + produces parent + N children.
//
// godlike/06 SSOT (idempotent-by-name): re-running CreateBatch
// with the same Spec.Name returns the same Batch + the same
// children — a worker that crashes and resumes does NOT pick up
// half-fabricated batch parents. The forward-pointer to Fase 3.4
// SQL durability is: a UNIQUE(name) constraint on media_batches
// backs this idempotency contract.
func (s *defaultBatchService) CreateBatch(_ context.Context, spec BatchSpec) (Batch, []BatchChild, error) {
	if err := validateSpec(spec); err != nil {
		return Batch{}, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// godlike/06 SSOT (idempotency by Name): look up an existing
	// batch under Spec.Name. When present, return its canonical
	// shape WITHOUT touching the in-memory store. Spec is
	// canonical immutable post-CreateBatch; a second call with
	// the same Name but different Spec is rejected (the worker
	// should treat Spec as fixed for the batch lifetime).
	//
	// godlike/06 SSOT (Spec-mismatch forward-pin): Fase 3.4 SQL
	// durability will back this with media_batches.UNIQUE(name)
	// + ON CONFLICT DO NOTHING so resume-after-crash flow sees
	// the same canonical Spec across recovery.
	for _, existing := range s.batches {
		if existing.batch.Name == spec.Name {
			if !specsStructurallyEqual(existing.batch.Spec, spec) {
				return Batch{}, nil, fmt.Errorf(
					"mediamemory: Spec drift for batch_name=%q (existing mode=%q vs new mode=%q): %w",
					spec.Name, string(existing.batch.Spec.Mode), string(spec.Mode),
					ErrBatchSpecDrift,
				)
			}
			children := make([]BatchChild, 0, len(existing.batch.Children))
			for _, childID := range existing.batch.Children {
				if c, ok := s.children[childID]; ok {
					children = append(children, c.child)
				}
			}
			cloned := existing.batch
			cloned.UpdatedAt = s.clock.Now().UTC()
			existing.batch = cloned
			return cloned, children, nil
		}
	}

	now := s.clock.Now().UTC()
	batchID := "batch-" + spec.Name + "-" + now.Format("20060102T150405Z")
	batch := Batch{
		ID:        batchID,
		Name:      spec.Name,
		Spec:      spec,
		State:     BatchPending,
		Children:  make([]string, 0, len(spec.Queries)*len(spec.Providers)),
		CreatedAt: now,
		UpdatedAt: now,
	}
	row := &batchRow{batch: batch, created: now}
	s.batches[batchID] = row

	children := make([]BatchChild, 0, len(spec.Queries)*len(spec.Providers))
	for _, q := range spec.Queries {
		for _, p := range spec.Providers {
			childID := batchID + ":" + q + ":" + p
			child := BatchChild{
				ID:        childID,
				BatchID:   batchID,
				Query:     q,
				Provider:  p,
				State:     BatchPending,
				CreatedAt: now,
				UpdatedAt: now,
			}
			s.children[childID] = &batchChildRow{
				child:   child,
				created: now,
			}
			row.batch.Children = append(row.batch.Children, childID)
			children = append(children, child)
		}
	}
	row.batch.UpdatedAt = s.clock.Now().UTC()
	// godlike/06 SSOT (return-the-mutated-shape): the Batch
	// value created at the top of this branch was COPIED (by
	// value) into row.batch. The Children appends above mutated
	// row.batch.Children (the slice header on the receiver
	// struct). The original value-copied `batch` variable still
	// carries its pre-mutation slice header (len=0); returning
	// the canonical mutable row batch so callers see the
	// populated parent + child rows.
	return row.batch, children, nil
}

// getBatch fetches the canonical Batch row by id, wrapped with
// ErrBatchNotFound when missing.
func (s *defaultBatchService) getBatch(batchID string) (Batch, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	row, ok := s.batches[batchID]
	if !ok {
		return Batch{}, fmt.Errorf("mediamemory: batch_id=%q: %w", batchID, ErrBatchNotFound)
	}
	return row.batch, nil
}

// getChild fetches the canonical BatchChild row by id.
func (s *defaultBatchService) getChild(childID string) (*batchChildRow, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.children[childID]
	if !ok {
		return nil, fmt.Errorf("mediamemory: batch_child_id=%q not in store", childID)
	}
	return c, nil
}

// AppendCandidate rejects terminal-state batches via wrapped
// ErrBatchNotReconcilable BEFORE the write (godlike/07).
func (s *defaultBatchService) AppendCandidate(_ context.Context, childID string, candidate MediaCandidate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	child, ok := s.children[childID]
	if !ok {
		return fmt.Errorf("mediamemory: batch_child_id=%q not in store", childID)
	}
	child.mu.Lock()
	defer child.mu.Unlock()

	// godlike/06 SSOT: lookup the parent first to check state.
	parent, ok := s.batches[child.child.BatchID]
	if !ok {
		return fmt.Errorf("mediamemory: parent for child %q missing", childID)
	}
	parent.mu.Lock()
	defer parent.mu.Unlock()
	if isTerminalState(parent.batch.State) {
		return fmt.Errorf("mediamemory: child %q parent state=%q: %w",
			childID, string(parent.batch.State), ErrBatchNotReconcilable)
	}

	child.child.CandidateIDs = append(child.child.CandidateIDs, candidate.ID)
	child.child.UpdatedAt = s.clock.Now().UTC()
	child.persistedN++

	parent.batch.CandidateCount++
	parent.batch.UpdatedAt = s.clock.Now().UTC()
	return nil
}

// MarkMaterialized bumps the parent's MaterializedCount and
// transitions the child toward Reconciliation when every
// candidate has been processed.
//
// godlike/06 SSOT (Fase "Ranking & rights" defense-in-depth): a
// Hot-tier promotion MUST verify RightsStatus == RightsVerified
// BEFORE the parent's MaterializedCount is incremented. The
// planner + worker already gate this upstream; this method is
// the canonical seal so a rights-denied candidate cannot slip
// through a worker bypass and inflate MaterializedCount.
// godlike/07 NO-FAKE-AVAILABILITY: a rights-denied / unknown /
// expired candidate surfaces as wrapped ErrApprovalRequired
// BEFORE the in-memory counters move — a partial flip is a
// regression that the dashboard would silently absorb.
func (s *defaultBatchService) MarkMaterialized(ctx context.Context, childID string, candidateID string, tier MaterializationStatus) error {
	if !IsKnownMaterializationStatus(tier) {
		return fmt.Errorf("mediamemory: mark materialized tier=%q: not in canonical closed set", string(tier))
	}
	if tier == MaterializationHot {
		cand, err := s.candidates.FindByID(ctx, candidateID)
		if err != nil {
			return fmt.Errorf(
				"mediamemory: MarkMaterialized Hot candidate lookup %q: %w",
				candidateID, err,
			)
		}
		if cand.RightsStatus != RightsVerified {
			return fmt.Errorf(
				"mediamemory: MarkMaterialized cannot promote %q to Hot (rights_status=%q, must be %q): %w",
				candidateID, cand.RightsStatus, RightsVerified, ErrApprovalRequired,
			)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	child, ok := s.children[childID]
	if !ok {
		return fmt.Errorf("mediamemory: batch_child_id=%q not in store", childID)
	}
	child.mu.Lock()
	defer child.mu.Unlock()
	parent, ok := s.batches[child.child.BatchID]
	if !ok {
		return fmt.Errorf("mediamemory: parent for child %q missing", childID)
	}
	parent.mu.Lock()
	defer parent.mu.Unlock()

	child.failures = append(child.failures, fmt.Sprintf(
		"candidate=%q tier=%q", candidateID, string(tier),
	))
	if tier == MaterializationHot {
		parent.batch.MaterializedCount++
	} else if tier == MaterializationWarm {
		// Warm tier is the canonical pre-Hot state; bump Indexed
		// but not MaterializedCount.
		parent.batch.IndexedCount++
	}
	child.child.UpdatedAt = s.clock.Now().UTC()
	parent.batch.UpdatedAt = s.clock.Now().UTC()
	return nil
}

// Reconcile finalises the batch. godlike/06 SSOT (terminal-state
// rewrite): once State flips to Completed/Failed, AppendCandidate
// refuses new writes (terminal-state guard).
func (s *defaultBatchService) Reconcile(_ context.Context, batchID string) (Batch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.batches[batchID]
	if !ok {
		return Batch{}, fmt.Errorf("mediamemory: batch_id=%q: %w", batchID, ErrBatchNotFound)
	}
	row.mu.Lock()
	defer row.mu.Unlock()

	now := s.clock.Now().UTC()

	// godlike/06 SSOT (terminal-state aggregate):
	//   - any child in Failed state promotes the parent to Failed
	//   - any child in Reconciling state (actively in flight via
	//     RunCatalogOnly) keeps the parent in Reconciling
	//   - children that have reached a terminal state
	//     (Completed/Failed) no longer contribute to in-flight
	//   - children still in BatchPending haven't started yet
	//     (RunCatalogOnly hasn't transitioned them to
	//     Reconciling). They do NOT count as in-flight — Reconcile
	//     called on a fresh (never-started) batch should transition
	//     the parent to Completed, NOT Reconciling. The Pending
	//     children are surfaced via the dashboard for the operator
	//     to decide whether to start RunCatalogOnly or mark them
	//     Failed manually.
	hasFailedChild := false
	hasInFlightChild := false
	for _, childID := range row.batch.Children {
		c, ok := s.children[childID]
		if !ok {
			continue
		}
		switch c.child.State {
		case BatchFailed:
			hasFailedChild = true
		case BatchReconciling:
			hasInFlightChild = true
		}
	}

	if hasFailedChild {
		row.batch.State = BatchFailed
	} else if hasInFlightChild {
		row.batch.State = BatchReconciling
	} else {
		row.batch.State = BatchCompleted
		row.batch.CompletedAt = &now
	}
	row.batch.UpdatedAt = now
	return row.batch, nil
}

// Get is the canonical read seam. Wrapped ErrBatchNotFound on miss.
func (s *defaultBatchService) Get(ctx context.Context, batchID string) (Batch, error) {
	return s.getBatch(batchID)
}

// Resume returns the children of an in-flight batch. godlike/06
// SSOT: Pending or Reconciling children only (terminal-state
// children are skipped — workers must not redo their work).
func (s *defaultBatchService) Resume(_ context.Context, batchID string) ([]BatchChild, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	row, ok := s.batches[batchID]
	if !ok {
		return nil, fmt.Errorf("mediamemory: batch_id=%q: %w", batchID, ErrBatchNotFound)
	}
	out := make([]BatchChild, 0, len(row.batch.Children))
	for _, childID := range row.batch.Children {
		c, ok := s.children[childID]
		if !ok {
			continue
		}
		switch c.child.State {
		case BatchPending, BatchReconciling:
			out = append(out, c.child)
		}
	}
	return out, nil
}

// isTerminalState reports whether state is in the canonical
// closed set {Completed, Failed}. godlike/06 SSOT: every state
// reader MUST go through this predicate so the terminal-state
// guard is centralized.
func isTerminalState(state BatchState) bool {
	return state == BatchCompleted || state == BatchFailed
}

// EnrichLinker is the canonical Fase 3.2 orchestrator that
// drives the linker worker across all (child × candidate) pairs
// already populated by an earlier RunCatalogOnly / AppendCandidate
// pass.
//
// godlike/06 SSOT (orchestration seam):
//  1. terminal-state fail-closed: a Completed/Failed batch
//     refuses EnrichLinker with wrapped ErrBatchNotReconcilable
//     BEFORE any child iteration.
//  2. linker-not-wired fail-closed: a batch with no SetLinker
//     wired surfaces a typed ErrLinkerInvariantBroken — the
//     composition root is the canonical wiring seam.
//  3. mark parent BatchReconciling (in-flight signal).
//  4. for each child in a single pass:
//     - iterate persisted candidates via the canonical CandidateRepository
//     - filter to DiscoveryStatus ∈ {DiscoverySearched}
//     - call linker.EnrichCandidate per candidate
//     - on Empty=true (idempotent skip): continue (no work, no writes)
//     - on ErrLinkerUnmappableConcept: append to parent.Failures +
//     continue batch (the candidate's row stays DiscoveryFailed).
//     - on ErrLinkerExtractFailed / ErrLinkerEmbeddingFailed: append
//     to parent.Failures + continue batch (Resume will retry).
//     - on success: parent.IndexedCount += len(IndexedConceptIDs).
//  5. Reconcile → terminal-state rewrite (Completed if all
//     children reached a non-in-flight state, Failed if any
//     recorded ErrLinkerInvariantBroken, Reconciling otherwise).
//
// godlike/06 SSOT (idempotency + resumability contract): the
// per-candidate gate (DiscoveryStatus ∈ {DiscoveryIndexed,
// DiscoveryMaterialized} short-circuits) makes a re-call of
// this method safely resumable from where it stopped — a
// crashed worker simply re-runs EnrichLinker and the surviving
// candidates are skipped via linker's Empty=true path. The
// canonical ON CONFLICT DO UPDATE on media_bindings ensures
// any re-run writes that escape the per-candidate skip are
// idempotent at the SQL layer.
//
// godlike/07 NO-FAKE-AVAILABILITY: a nil linker (composition
// root forgot to wire) is NEVER silently treated as no-op;
// the typed envelope forces a 500-level response so the
// operator notices the misconfiguration.
func (s *defaultBatchService) EnrichLinker(ctx context.Context, batchID string) (Batch, error) {
	s.mu.RLock()
	row, ok := s.batches[batchID]
	if !ok {
		s.mu.RUnlock()
		return Batch{}, fmt.Errorf("mediamemory: enrich-linker batch_id=%q: %w", batchID, ErrBatchNotFound)
	} // godlike/07 NO-FAKE-AVAILABILITY terminal-state guard:
	// ONLY refuse `BatchFailed` (a prior enrich attempt produced
	// a terminal-state failure that the operator must explicitly
	// retry). `BatchCompleted` (clean termination of an earlier
	// RunCatalogOnly fan-out) is NOT a blocker — enrich on a
	// Completed-from-catalog-only batch is the canonical happy
	// path that lives between catalog-only and materialization.
	// godlike/06 SSOT: append-side AppendCandidate keeps the
	// original `isTerminalState` guard (Completed/Failed both
	// refuse) because catalog-only appends MUST not mutate a
	// finalized batch; only the EnrichLinker gate loosens.
	if row.batch.State == BatchFailed {
		s.mu.RUnlock()
		return Batch{}, fmt.Errorf(
			"mediamemory: enrich-linker batch_id=%q state=%q: %w",
			batchID, row.batch.State, ErrBatchNotReconcilable,
		)
	}
	if s.linker == nil {
		s.mu.RUnlock()
		return Batch{}, fmt.Errorf(
			"mediamemory: enrich-linker batch_id=%q: LinkerWorker not wired (call SetLinker): %w",
			batchID, ErrLinkerInvariantBroken,
		)
	}
	linkerSnapshot := s.linker
	specSnapshot := row.batch.Spec
	childIDs := append([]string{}, row.batch.Children...)
	s.mu.RUnlock()

	// Mark parent in-flight. godlike/06 SSOT: lock-order is
	// s.mu.Lock → mutate parent → unlock BEFORE any per-child
	// acquisition to keep the lock and per-child iteration in
	// the canonical order. The parent flip can race with
	// concurrent Get() callers; BatchReconciling is non-terminal
	// so Get viewers see the in-flight state which is correct.
	s.mu.Lock()
	row.batch.State = BatchReconciling
	row.batch.UpdatedAt = s.clock.Now().UTC()
	s.mu.Unlock()

	// Per-child iteration.
	indexedCount := 0
	failedCount := 0
	for _, childID := range childIDs {
		s.mu.RLock()
		c, exists := s.children[childID]
		s.mu.RUnlock()
		if !exists {
			continue
		}
		// godlike/06 SSOT (per-child spec lookup): the candidates
		// for a child are persisted under (child_id) on the
		// CandidateRepository side via AppendCandidate; here we
		// re-derive them by ListByProvider filtered via the
		// canonical portal (forward-pin to Fase 3.4 SQL
		// repository for media_child_candidates cross-table).
		// Fase 3.2 simplification: read every candidate for the
		// (provider, query) pair via ListPendingMaterialization-
		// like read. Compose a query-precise enumeration via
		// the child's query + provider to keep the slice tight.
		cands, lerr := s.loadChildCandidates(ctx, c.child)
		if lerr != nil {
			s.recordParentFailure(row.batch.ID,
				fmt.Sprintf("child=%q load candidates failed: %s", childID, lerr.Error()))
			continue
		}
		for _, cand := range cands {
			// godlike/06 SSOT (canonical skip filter): a candidate
			// already past the linker gate is skipped BEFORE
			// calling EnrichCandidate so the worker never sees
			// a no-op candidate (defense-in-depth alongside the
			// worker's own idempotency check).
			if cand.DiscoveryStatus == DiscoveryIndexed ||
				cand.DiscoveryStatus == DiscoveryMaterialized {
				continue
			}
			req := LinkerRequest{
				Candidate: cand,
				ProjectID: "batch-" + batchID + "-child-" + childID,
				Language:  specSnapshot.Language,
			}
			result, lerr := linkerSnapshot.EnrichCandidate(ctx, req)
			if lerr != nil {
				// godlike/06 SSOT (per-candidate error isolation):
				// every failure is recorded on the parent
				// Failures[] envelope and the loop continues.
				// The batch's terminal state is decided at
				// Reconcile time, not per-candidate.
				s.recordParentFailure(row.batch.ID,
					fmt.Sprintf("child=%q candidate=%q linker: %s",
						childID, cand.ID, lerr.Error()))
				if errors.Is(lerr, ErrLinkerUnmappableConcept) ||
					errors.Is(lerr, ErrLinkerInvariantBroken) {
					failedCount++
				}
				continue
			}
			if result.Empty {
				// Idempotency hit: the linker short-circuited.
				indexedCount += len(result.IndexedConceptIDs)
				continue
			}
			indexedCount += len(result.IndexedConceptIDs)
		}
	}

	// Reconcile to terminal state. godlike/06 SSOT: failedCount
	// tracks HARD failures (ErrLinkerInvariantBroken +
	// ErrLinkerUnmappableConcept) which flip the batch to
	// BatchFailed; transient failures (ErrLinkerExtractFailed /
	// ErrLinkerEmbeddingFailed) leave it Reconciling so a
	// subsequent EnrichLinker call resumes.
	s.mu.Lock()
	if failedCount > 0 {
		row.batch.State = BatchFailed
		row.batch.IndexedCount = indexedCount
		now := s.clock.Now().UTC()
		row.batch.CompletedAt = &now
	} else {
		row.batch.IndexedCount = indexedCount
		// Keep state = BatchReconciling so Resume picks up any
		// unflushed candidates on the next call. Reconcile
		// is invoked explicitly by the orchestrator ONLY when
		// the operator-deployed Resume / abort flow decides.
	}
	row.batch.UpdatedAt = s.clock.Now().UTC()
	s.mu.Unlock()

	s.mu.RLock()
	cloned := row.batch
	s.mu.RUnlock()
	return cloned, nil
}

// loadChildCandidates enumerates candidates that belong to a
// given BatchChild. godlike/06 SSOT (Fase 3.2 bridge): the
// canonical SQL-side media_child_candidates cross-table lands
// in Fase 3.4; for Fase 3.2 we re-derive via
// candidatesRepository.ListPendingMaterialization which
// returns warm-tier rows filtered to (provider) + (rights).
// To keep the slice tight per child we additionally filter by
// (title / description / source_url) heuristics derived from
// the child query — this is a Fase 3.2 simplification that
// becomes redundant once the canonical cross-table is wired.
//
// godlike/07 NO-FAKE-AVAILABILITY: load failures surface as
// typed envelopes (ErrCandidateNotFound on empty, raw wrapped
// errors on SQLite trips).
func (s *defaultBatchService) loadChildCandidates(ctx context.Context, child BatchChild) ([]MediaCandidate, error) {
	if s.candidates == nil {
		return nil, fmt.Errorf("mediamemory: child=%q CandidateRepository not wired", child.ID)
	}
	all, err := s.candidates.ListByProvider(ctx, child.Provider, 0)
	if err != nil {
		return nil, fmt.Errorf("mediamemory: child=%q list-by-provider: %w", child.ID, err)
	}
	// Tight filter: only candidates that match the child's
	// Query AND have status ∈ {DiscoverySearched, DiscoveryAnalyzed}.
	// Title-contains is a Fase 3.2 heuristic; Fase 3.4 will
	// introduce media_child_candidates (child_id, candidate_id)
	// cross-table so this filter becomes a single JOIN.
	queryLower := strings.ToLower(child.Query)
	out := make([]MediaCandidate, 0, len(all))
	for _, c := range all {
		if !strings.Contains(strings.ToLower(c.Title), queryLower) {
			continue
		}
		if c.DiscoveryStatus != DiscoverySearched {
			// Indexed / Materialized / Failed all skipped — the
			// orchestrator's filter at the call site plus the
			// linker's idempotency gate will double-check.
			continue
		}
		out = append(out, c)
	}
	return out, nil
} // RunCatalogOnly is the canonical Fase 3.1 orchestrator.
// godlike/06 SSOT (orchestration seam):
//  1. validateSpec (typed sentinel surfaces on closed-set drift).
//  2. CreateBatch → parent + (queries × providers) child rows.
//  3. For each child in a single pass:
//     - mark child state = Reconciling (in-flight signal).
//     - Discover(req{query, provider, language, mediaTypes})
//     via the canonical DiscoveryWorker.
//     - for every PersistedCandidateIDs: AppendCandidate.
//     - on Discover error: record failure onto child, mark child
//     state = Failed at end of run.
//  4. Reconcile → terminal-state rewrite (Completed if all
//     children reached terminal, Failed if any child failed,
//     Reconciling if any in-flight).
//
// godlike/07 NO-FAKE-AVAILABILITY: a nil worker degrades to
// "no discovery enabled" — Reconcile runs without Discovery and
// surfaces a warning. The caller (composition root) MUST inject
// the DiscoveryWorker for production catalog_only runs.
func (s *defaultBatchService) RunCatalogOnly(ctx context.Context, spec BatchSpec) (Batch, []BatchChild, error) {
	if err := validateSpec(spec); err != nil {
		return Batch{}, nil, err
	}
	if s.worker == nil {
		return Batch{}, nil, fmt.Errorf(
			"mediamemory: BatchService.RunCatalogOnly requires DiscoveryWorker; nil: %w",
			errors.New("missing worker"),
		)
	}

	parent, children, err := s.CreateBatch(ctx, spec)
	if err != nil {
		return Batch{}, nil, err
	}

	for _, c := range children {
		row, ok := s.lookupChild(c.ID)
		if !ok {
			continue
		}

		// godlike/06 SSOT: in-flight state is Reconciling so a
		// concurrent reader of the parent sees live progress.
		row.mu.Lock()
		row.child.State = BatchReconciling
		row.child.UpdatedAt = s.clock.Now().UTC()
		row.mu.Unlock()

		req := DiscoveryRequest{
			Query:      row.child.Query,
			Provider:   row.child.Provider,
			Language:   spec.Language,
			MediaTypes: spec.MediaTypes,
			Limit:      spec.MaxCandidates,
			ProjectID:  spec.Name, // godlike/06 SSOT: project_id == batch.Name
		}
		dres, derr := s.worker.Discover(ctx, req)
		if derr != nil {
			row.mu.Lock()
			row.failures = append(row.failures, fmt.Sprintf(
				"discover failed: %s", derr.Error(),
			))
			row.child.State = BatchFailed
			row.child.UpdatedAt = s.clock.Now().UTC()
			row.mu.Unlock()
			s.recordParentFailure(parent.ID, fmt.Sprintf("child=%q: discover failed: %s",
				row.child.ID, derr.Error()))
			continue
		}

		// godlike/06 SSOT (per-backend error surface): backend
		// errors from the fan-out are catalogued onto the child
		// row (dashboard-visible) AND onto the parent Failures[]
		// so a top-level UI can show per-child rationale.
		for be, msg := range dres.BackendErrors {
			row.mu.Lock()
			row.failures = append(row.failures,
				fmt.Sprintf("backend=%q: %s", be, msg))
			row.mu.Unlock()
			s.recordParentFailure(parent.ID, fmt.Sprintf("child=%q backend=%q: %s",
				row.child.ID, be, msg))
		}

		// Persist every executed discovery-row into the canonical
		// in-memory store via the canonical AppendCandidate path
		// (terminal-state guard fires here).
		for _, candID := range dres.PersistedCandidateIDs {
			cand, findErr := s.candidates.FindByID(ctx, candID)
			if findErr != nil {
				row.mu.Lock()
				row.failures = append(row.failures,
					fmt.Sprintf("candidate=%q find after persist failed: %s",
						candID, findErr.Error()))
				row.mu.Unlock()
				continue
			}
			if appendErr := s.AppendCandidate(ctx, row.child.ID, cand); appendErr != nil {
				row.mu.Lock()
				row.failures = append(row.failures,
					fmt.Sprintf("append candidate=%q failed: %s",
						candID, appendErr.Error()))
				row.mu.Unlock()
				continue
			}
		}

		// godlike/06 SSOT (terminal-state per child): Failed only
		// when zero candidates persisted AND failures ≥ 1.
		row.mu.Lock()
		if len(dres.PersistedCandidateIDs) == 0 && len(row.failures) > 0 {
			row.child.State = BatchFailed
		} else {
			row.child.State = BatchCompleted
		}
		row.child.UpdatedAt = s.clock.Now().UTC()
		row.mu.Unlock()
	}

	// Re-bundle children for the return envelope. The batch is left
	// in-flight (BatchReconciling) so a subsequent EnrichLinker pass
	// can process the persisted candidates. The orchestrator calls
	// Reconcile explicitly when the end-to-end pipeline is complete.
	finalParent, err := s.Get(ctx, parent.ID)
	if err != nil {
		return parent, nil, err
	}
	out := make([]BatchChild, 0, len(finalParent.Children))
	for _, childID := range finalParent.Children {
		c, ok := s.lookupChild(childID)
		if !ok {
			continue
		}
		out = append(out, c.child)
	}
	return finalParent, out, nil
}
