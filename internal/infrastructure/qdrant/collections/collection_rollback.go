package collections

import (
	"context"
	"fmt"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

// RollbackCandidate is a compatibility wrapper for the pre-state-machine
// API. It resolves the registered ACTIVE projection and delegates to the
// canonical RollbackProjection operation.
func (cm *CollectionManager) RollbackCandidate(ctx context.Context, currentTarget, rollbackTarget string) error {
	projectionID, ok := cm.projectionByCollection(currentTarget)
	if !ok {
		return fmt.Errorf("%w: current collection %q is not registered", ErrProjectionNotFound, currentTarget)
	}
	projection, _ := cm.Projection(projectionID)
	if projection.Status != string(capregistry.ProjectionActive) {
		return fmt.Errorf("%w: current collection %q has status %s", ErrProjectionInvalidState, currentTarget, projection.Status)
	}
	return cm.RollbackProjection(ctx, projectionID, rollbackTarget)
}

// SwitchAlias is retained only as a migration wrapper. It never calls the
// transport client directly: activation and rollback must pass through the
// Projection Manager state machine.
func (cm *CollectionManager) SwitchAlias(ctx context.Context, oldTarget, newTarget string) error {
	if oldTarget == newTarget {
		return nil
	}
	if newID, ok := cm.projectionByCollection(newTarget); ok {
		newProjection, _ := cm.Projection(newID)
		if newProjection.Status == string(capregistry.ProjectionReady) {
			return cm.ActivateProjection(ctx, newID, newProjection.SourceRegistrySeq)
		}
	}
	return cm.RollbackCandidate(ctx, oldTarget, newTarget)
}
