// Package mediamemory — batch_service_persistence.go owns the
// minimal-viable in-memory store behind BatchService: CreateBatch,
// AppendCandidate, MarkMaterialized, plus the read helpers
// (getBatch / getChild) used by orchestrators and lifecycle methods.
//
// godlike/06 SSOT (single canonical home per responsibility):
// every state-mutating path lives here so the Fase 3.4 SQLite
// swap (media_batches + media_batch_children + media_child_candidates)
// is mechanical — DROP-IN the repository-backed implementation
// in this file without touching any other file.
//
// godlike/06 SSOT (terminal-state guard): AppendCandidate writes
// MUST be guarded by isTerminalState BEFORE the in-memory mutation
// so a roll-forward race cannot produce a partial append on a
// batch that Reconcile already marked Completed/Failed.
//
// File split ownership (godlike/06 SSOT):
//   - batch_service.go                : BatchService port + struct + ctors + lifecycle wiring
//   - batch_service_validation.go     : validateSpec + specsStructurallyEqual + isTerminalState
//   - batch_service_persistence.go    : CreateBatch/AppendCandidate/MarkMaterialized/internal reads  ← this file
//   - batch_service_lifecycle.go      : Get/Resume/Reconcile (terminal-state machine)
//   - batch_service_orchestrator.go   : RunCatalogOnly/EnrichLinker/loadChildCandidates
//   - batch_materialization.go        : MaterializeTopK/PromoteOnDemand/recordParentFailure (Fase 3.3)
package mediamemory

import (
	"context"
	"fmt"
)

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
