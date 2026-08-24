// Package mediamemory — batch_service_lifecycle.go owns the
// terminal-state machine transitions for BatchService: Get
// (canonical read seam), Resume (recover in-flight children),
// Reconcile (finalise parent state).
//
// godlike/06 SSOT (single canonical home per responsibility):
// every state-changing read passes through these methods in
// lockstep with batch_service_persistence.go so the canonical
// "AppendCandidate refuses after Reconcile/Completed" rule is
// preserved across all callers.
//
// File split ownership (godlike/06 SSOT):
//   - batch_service.go                : BatchService port + struct + ctors + lifecycle wiring
//   - batch_service_validation.go     : validateSpec + specsStructurallyEqual + isTerminalState
//   - batch_service_persistence.go    : CreateBatch/AppendCandidate/MarkMaterialized/internal reads
//   - batch_service_lifecycle.go      : Get/Resume/Reconcile (terminal-state machine)  ← this file
//   - batch_service_orchestrator.go   : RunCatalogOnly/EnrichLinker/loadChildCandidates
//   - batch_materialization.go        : MaterializeTopK/PromoteOnDemand/recordParentFailure (Fase 3.3)
package mediamemory

import (
	"context"
	"fmt"
)

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
