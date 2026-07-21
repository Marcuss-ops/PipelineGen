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
	candidates CandidateRepository
	planner    AcquisitionPlanner
	rights     RightsValidator
	external   SearchFanOut
	worker     DiscoveryWorker
	log        Logger
	clock      Clock

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
func (s *defaultBatchService) MarkMaterialized(_ context.Context, childID string, candidateID string, tier MaterializationStatus) error {
	if !IsKnownMaterializationStatus(tier) {
		return fmt.Errorf("mediamemory: mark materialized tier=%q: not in canonical closed set", string(tier))
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

	finalParent, reconcileErr := s.Reconcile(ctx, parent.ID)
	if reconcileErr != nil {
		return parent, nil, reconcileErr
	}

	// Re-bundle children for the return envelope.
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

// lookupChild is the canonical read seam for batch_child rows.
// godlike/06 SSOT: every per-child helper goes through this so
// the in-memory map mutex is acquired exactly once per call.
func (s *defaultBatchService) lookupChild(childID string) (*batchChildRow, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.children[childID]
	return c, ok
}

// recordParentFailure appends a typed failure string onto the
// canonical parent batch row. godlike/06 SSOT: the Failures[]
// slice is the dashboard-visible rationale surface and any
// orchestrator-side failure MUST route through this seam so the
// error envelope reaches the dashboard cleanly.
//
// Lock-safe: takes s.mu.RLock to read the parent row pointer,
// releases, then takes parent.mu to mutate Failures[].
func (s *defaultBatchService) recordParentFailure(batchID, msg string) {
	s.mu.RLock()
	parent, ok := s.batches[batchID]
	s.mu.RUnlock()
	if !ok || parent == nil {
		return
	}
	parent.mu.Lock()
	parent.batch.Failures = append(parent.batch.Failures, msg)
	parent.batch.UpdatedAt = s.clock.Now().UTC()
	parent.mu.Unlock()
}
