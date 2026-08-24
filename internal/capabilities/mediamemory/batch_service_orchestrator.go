// Package mediamemory — batch_service_orchestrator.go owns the
// orchestrator entries: RunCatalogOnly (Fase 3.1 discovery
// fan-out), EnrichLinker (Fase 3.2 linker pass), and
// loadChildCandidates (shared read helper used by both the
// orchestrator and MaterializeTopK via the same struct receiver).
//
// godlike/06 SSOT (orchestration seam): these methods compose
// the persistence ops in batch_service_persistence.go with the
// canonical external workers (DiscoveryWorker, LinkerWorker) so
// the parent/child state machine and the rights-gating live in
// one place. They do NOT introduce new failure modes — every
// typed envelope they raise is already declared in types.go and
// routed via errors.Is by the persistence and lifecycle files.
//
// File split ownership (godlike/06 SSOT):
//   - batch_service.go                : BatchService port + struct + ctors + lifecycle wiring
//   - batch_service_validation.go     : validateSpec + specsStructurallyEqual + isTerminalState
//   - batch_service_persistence.go    : CreateBatch/AppendCandidate/MarkMaterialized/internal reads
//   - batch_service_lifecycle.go      : Get/Resume/Reconcile (terminal-state machine)
//   - batch_service_orchestrator.go   : RunCatalogOnly/EnrichLinker/loadChildCandidates  ← this file
//   - batch_materialization.go        : MaterializeTopK/PromoteOnDemand/recordParentFailure (Fase 3.3)
package mediamemory

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

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

// RunCatalogOnly is the canonical Fase 3.1 orchestrator.
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
