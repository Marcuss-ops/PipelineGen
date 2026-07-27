package mediamemory

import (
	"context"
	"fmt"
)

func (s *defaultBatchService) lookupChild(childID string) (*batchChildRow, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.children[childID]
	return c, ok
}

func (s *defaultBatchService) MaterializeTopK(ctx context.Context, batchID string) (Batch, error) {
	s.mu.RLock()
	row, ok := s.batches[batchID]
	if !ok {
		s.mu.RUnlock()
		return Batch{}, fmt.Errorf("mediamemory: materialize-top-k batch_id=%q: %w", batchID, ErrBatchNotFound)
	}
	if row.batch.State == BatchCompleted || row.batch.State == BatchFailed {
		ro := row.batch
		s.mu.RUnlock()
		return Batch{}, fmt.Errorf("mediamemory: materialize-top-k batch_id=%q state=%q: %w", batchID, ro.State, ErrBatchNotReconcilable)
	}
	if s.planner == nil || s.materializeWorker == nil {
		s.mu.RUnlock()
		return Batch{}, fmt.Errorf("mediamemory: materialize-top-k batch_id=%q: AcquisitionPlanner or MaterializeWorker not wired: %w", batchID, ErrLinkerInvariantBroken)
	}
	plannerSnapshot, workerSnapshot := s.planner, s.materializeWorker
	specSnapshot := row.batch.Spec
	childIDs := append([]string{}, row.batch.Children...)
	s.mu.RUnlock()

	s.mu.Lock()
	row.batch.State = BatchReconciling
	row.batch.UpdatedAt = s.clock.Now().UTC()
	s.mu.Unlock()

	seen := make(map[string]MediaCandidate, 1024)
	for _, childID := range childIDs {
		s.mu.RLock()
		c, exists := s.children[childID]
		s.mu.RUnlock()
		if !exists {
			continue
		}
		cands, lerr := s.loadChildCandidates(ctx, c.child)
		if lerr != nil {
			s.recordParentFailure(batchID, fmt.Sprintf("materialize-top-k child=%q load candidates failed: %s", childID, lerr.Error()))
			continue
		}
		for _, cand := range cands {
			if cand.MaterializationStatus != MaterializationCold {
				continue
			}
			if _, dup := seen[cand.ID]; !dup {
				seen[cand.ID] = cand
			}
		}
	}
	agg := make([]MediaCandidate, 0, len(seen))
	for _, c := range seen {
		agg = append(agg, c)
	}
	promotes, perr := plannerSnapshot.Plan(ctx, AcquisitionInput{BatchID: batchID, TopK: specSnapshot.MaterializeTopK, Candidates: agg})
	if perr != nil {
		s.recordParentFailure(batchID, fmt.Sprintf("materialize-top-k batch_id=%q plan failed: %s", batchID, perr.Error()))
	}

	var matRes MaterializationResult
	if len(promotes) > 0 {
		var merr error
		matRes, merr = workerSnapshot.Materialize(ctx, MaterializationRequest{BatchID: batchID, ProjectID: specSnapshot.Name, Promotes: promotes, HotCache: false})
		if merr != nil {
			s.recordParentFailure(batchID, fmt.Sprintf("materialize-top-k batch_id=%q worker failed: %s", batchID, merr.Error()))
		}
	}
	s.mu.Lock()
	for range matRes.PersistedAssetIDs {
		parent, ok := s.batches[batchID]
		if !ok {
			continue
		}
		parent.mu.Lock()
		parent.batch.IndexedCount++
		parent.batch.UpdatedAt = s.clock.Now().UTC()
		parent.mu.Unlock()
	}
	for _, msg := range matRes.Failures {
		s.recordParentFailure(batchID, msg)
	}
	s.mu.Unlock()

	s.mu.RLock()
	cloned := row.batch
	s.mu.RUnlock()
	return cloned, nil
}

func (s *defaultBatchService) PromoteOnDemand(ctx context.Context, candidate MediaCandidate, opts MaterializeOptions) (MediaCandidate, error) {
	s.mu.RLock()
	mw := s.materializeWorker
	s.mu.RUnlock()
	if mw == nil {
		return candidate, fmt.Errorf("mediamemory: PromoteOnDemand: MaterializeWorker not wired (call SetMaterializeWorker): %w", ErrLinkerInvariantBroken)
	}
	mat, err := mw.PromoteOnDemand(ctx, candidate, opts)
	if err != nil {
		return candidate, fmt.Errorf("mediamemory: PromoteOnDemand candidate=%q: %w", candidate.ID, err)
	}
	return mat, nil
}

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
