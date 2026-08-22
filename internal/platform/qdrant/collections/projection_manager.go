package collections

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
)

var (
	ErrProjectionNotFound       = errors.New("qdrant projection: projection not found")
	ErrProjectionInvalidState   = errors.New("qdrant projection: invalid state")
	ErrProjectionNotReady       = errors.New("qdrant projection: projection is not ready")
	ErrProjectionAlreadyExists  = errors.New("qdrant projection: projection already exists")
	ErrProjectionRollbackTarget = errors.New("qdrant projection: rollback target is required")
)

// BeginProjection records a new BUILDING projection. ProjectionID is a
// logical build identity and must not be reused after a failed build.
func (cm *CollectionManager) BeginProjection(ctx context.Context, projectionID, collection string, registrySequence int64) error {
	projectionID = strings.TrimSpace(projectionID)
	collection = strings.TrimSpace(collection)
	if projectionID == "" || collection == "" || registrySequence < 0 {
		return fmt.Errorf("%w: projection_id, collection and non-negative registry sequence are required", ErrProjectionInvalidState)
	}
	resolvedSequence, err := cm.resolveRegistrySequence(ctx, registrySequence)
	if err != nil {
		return err
	}

	cm.projectionMu.Lock()
	defer cm.projectionMu.Unlock()
	if _, exists := cm.projections[projectionID]; exists {
		return fmt.Errorf("%w: %q", ErrProjectionAlreadyExists, projectionID)
	}
	projection := capregistry.Projection{
		ProjectionID:      projectionID,
		ProjectionType:    "qdrant",
		CollectionName:    collection,
		AliasName:         cm.schema.RuntimeAlias,
		Status:            string(capregistry.ProjectionBuilding),
		SourceRegistrySeq: resolvedSequence,
		CreatedAt:         time.Now().UTC().Format(time.RFC3339Nano),
	}
	cm.projections[projectionID] = projection
	if cm.registryLedger != nil {
		if err := cm.registryLedger.RegisterProjection(ctx, projection); err != nil {
			delete(cm.projections, projectionID)
			return fmt.Errorf("persist projection %q as BUILDING: %w", projectionID, err)
		}
	}
	return nil
}

// Projection returns a snapshot of lifecycle metadata. The returned value is
// detached from the manager and safe for callers to retain.
func (cm *CollectionManager) Projection(projectionID string) (capregistry.Projection, bool) {
	cm.projectionMu.RLock()
	defer cm.projectionMu.RUnlock()
	projection, ok := cm.projections[projectionID]
	return projection, ok
}

// GetStatus returns the explicit persisted/in-memory lifecycle state.
func (cm *CollectionManager) GetStatus(projectionID string) (capregistry.ProjectionStatus, error) {
	projection, err := cm.requireProjection(projectionID)
	if err != nil {
		return "", err
	}
	return capregistry.ProjectionStatus(projection.Status), nil
}

// Build is the concise canonical operation name. BuildProjection remains
// available as a descriptive compatibility spelling.
func (cm *CollectionManager) Build(ctx context.Context, projectionID, collection string, registrySequence int64) error {
	return cm.BuildProjection(ctx, projectionID, collection, registrySequence)
}

// BuildProjection creates the physical candidate without touching the runtime
// alias. Any preparation failure permanently marks this build FAILED; callers
// must start a new projection identity to retry.
func (cm *CollectionManager) BuildProjection(ctx context.Context, projectionID, collection string, registrySequence int64) error {
	return cm.BuildProjectionWith(ctx, projectionID, collection, registrySequence, nil)
}

// BuildProjectionWith creates a physical candidate and, when populate is
// supplied, fills it through the caller's canonical index writer before the
// candidate can be validated. The manager still owns the BUILDING state and
// fail-closed transition on either preparation or population failure.
func (cm *CollectionManager) BuildProjectionWith(ctx context.Context, projectionID, collection string, registrySequence int64, populate ProjectionPopulateFunc) error {
	resolvedSequence, err := cm.resolveRegistrySequence(ctx, registrySequence)
	if err != nil {
		return err
	}
	if err := cm.BeginProjection(ctx, projectionID, collection, resolvedSequence); err != nil {
		return err
	}
	if err := cm.PrepareCandidate(ctx, collection); err != nil {
		if failErr := cm.failProjection(ctx, projectionID); failErr != nil {
			return fmt.Errorf("build projection %q: %w; persist FAILED state: %v", projectionID, err, failErr)
		}
		return fmt.Errorf("build projection %q: %w", projectionID, err)
	}
	if populate != nil {
		if err := populate(ctx, collection); err != nil {
			if failErr := cm.failProjection(ctx, projectionID); failErr != nil {
				return fmt.Errorf("populate projection %q: %w; persist FAILED state: %v", projectionID, err, failErr)
			}
			return fmt.Errorf("populate projection %q: %w", projectionID, err)
		}
	}
	return nil
}

// RebuildProjection starts a fresh build identity. A failed or retired
// projection is never revived in place; callers must pass a new projectionID
// and collection name. The previous projection remains available for rollback
// until an explicit retention policy retires it.
func (cm *CollectionManager) RebuildProjection(ctx context.Context, projectionID, collection string, registrySequence int64, populate ProjectionPopulateFunc) error {
	return cm.BuildProjectionWith(ctx, projectionID, collection, registrySequence, populate)
}

// Rebuild is the concise semantic alias for callers that use the manager as a
// capability rather than the concrete CollectionManager.
func (cm *CollectionManager) Rebuild(ctx context.Context, projectionID, collection string, registrySequence int64, populate ProjectionPopulateFunc) error {
	return cm.RebuildProjection(ctx, projectionID, collection, registrySequence, populate)
}

// Validate is the concise canonical operation name.
func (cm *CollectionManager) Validate(ctx context.Context, projectionID string, registrySequence int64, expectedPoints int) (*schema.SwitchReport, error) {
	return cm.ValidateProjection(ctx, projectionID, registrySequence, expectedPoints)
}

// ValidateProjection performs the full verifier gate when one is wired. A
// schema-only fallback is retained for tools that intentionally construct a
// collection manager without SQLite asset data. In both modes a stale or
// ahead projection is rejected before Qdrant validation and marked FAILED.
func (cm *CollectionManager) ValidateProjection(ctx context.Context, projectionID string, registrySequence int64, expectedPoints int) (*schema.SwitchReport, error) {
	projection, err := cm.requireProjection(projectionID)
	if err != nil {
		return nil, err
	}
	registrySequence, err = cm.resolveRegistrySequence(ctx, registrySequence)
	if err != nil {
		return nil, err
	}
	if err := cm.transitionProjection(ctx, projectionID, capregistry.ProjectionValidating); err != nil {
		return nil, err
	}
	if err := capregistry.ValidateProjectionSequence(projection.SourceRegistrySeq, registrySequence); err != nil {
		if failErr := cm.failProjection(ctx, projectionID); failErr != nil {
			return nil, fmt.Errorf("%w; persist FAILED state: %v", err, failErr)
		}
		return nil, err
	}

	cm.projectionMu.RLock()
	verifier := cm.reindexVerifier
	cm.projectionMu.RUnlock()
	if verifier != nil {
		report, verifyErr := verifier.VerifyReindex(ctx, projection.CollectionName, expectedPoints)
		if verifyErr != nil {
			failure := fmt.Errorf("validate projection %q: %w", projectionID, verifyErr)
			if failErr := cm.failProjection(ctx, projectionID); failErr != nil {
				return report, fmt.Errorf("%w; persist FAILED state: %v", failure, failErr)
			}
			return report, failure
		}
		if report == nil || !report.Ready {
			failure := fmt.Errorf("validate projection %q: %w", projectionID, ErrProjectionNotReady)
			if failErr := cm.failProjection(ctx, projectionID); failErr != nil {
				return report, fmt.Errorf("%w; persist FAILED state: %v", failure, failErr)
			}
			return report, failure
		}
		if err := cm.transitionProjection(ctx, projectionID, capregistry.ProjectionReady); err != nil {
			return report, err
		}
		return report, nil
	}

	if err := cm.VerifyCandidate(ctx, projection.CollectionName); err != nil {
		failure := fmt.Errorf("validate projection %q: %w", projectionID, err)
		if failErr := cm.failProjection(ctx, projectionID); failErr != nil {
			return nil, fmt.Errorf("%w; persist FAILED state: %v", failure, failErr)
		}
		return nil, failure
	}
	if err := cm.transitionProjection(ctx, projectionID, capregistry.ProjectionReady); err != nil {
		return nil, err
	}
	return nil, nil
}

// Activate is the concise canonical operation name.
func (cm *CollectionManager) Activate(ctx context.Context, projectionID string, registrySequence int64) error {
	return cm.ActivateProjection(ctx, projectionID, registrySequence)
}

// ActivateProjection switches the alias in one Qdrant action request. A
// transport failure leaves the projection READY and the previous alias
// untouched, making retry safe. A successful switch transitions READY→ACTIVE
// and retires the previously active build known to this manager.
func (cm *CollectionManager) ActivateProjection(ctx context.Context, projectionID string, registrySequence int64) error {
	projection, err := cm.requireProjection(projectionID)
	if err != nil {
		return err
	}
	registrySequence, err = cm.resolveRegistrySequence(ctx, registrySequence)
	if err != nil {
		return err
	}
	if projection.Status != string(capregistry.ProjectionReady) {
		return fmt.Errorf("%w: %q is %s", ErrProjectionNotReady, projectionID, projection.Status)
	}
	if err := capregistry.ValidateProjectionSequence(projection.SourceRegistrySeq, registrySequence); err != nil {
		if failErr := cm.failProjection(ctx, projectionID); failErr != nil {
			return fmt.Errorf("%w; persist FAILED state: %v", err, failErr)
		}
		return err
	}

	// Qdrant's batched alias request is atomic, but its API does not
	// compare the delete action with the target observed by this caller.
	// Serialize the complete observe/fence/switch/compensate sequence for
	// this manager and re-read the canonical registry immediately before
	// switching so stale builds fail closed.
	cm.aliasMu.Lock()
	defer cm.aliasMu.Unlock()

	oldTarget, err := cm.client.GetAliasTarget(ctx, projection.AliasName)
	if err != nil {
		var notFound *transport.ErrCollectionNotFound
		if !errors.As(err, &notFound) {
			return fmt.Errorf("resolve active projection alias: %w", err)
		}
		oldTarget = ""
	}
	latestSequence, err := cm.resolveRegistrySequence(ctx, registrySequence)
	if err != nil {
		return err
	}
	if err := capregistry.ValidateProjectionSequence(projection.SourceRegistrySeq, latestSequence); err != nil {
		if failErr := cm.failProjection(ctx, projectionID); failErr != nil {
			return fmt.Errorf("%w; persist FAILED state: %v", err, failErr)
		}
		return err
	}
	if err := cm.client.SwitchAlias(ctx, projection.AliasName, oldTarget, projection.CollectionName); err != nil {
		// Keep READY: the Qdrant action is atomic, so the old target remains
		// authoritative and retrying after the infrastructure fault is safe.
		return fmt.Errorf("activate projection %q: %w", projectionID, err)
	}

	if err := cm.transitionProjection(ctx, projectionID, capregistry.ProjectionActive); err != nil {
		rollbackErr := cm.restoreAlias(ctx, projection.AliasName, projection.CollectionName, oldTarget)
		if rollbackErr != nil {
			return fmt.Errorf("activate projection %q: persist ACTIVE failed: %v; alias rollback failed: %w", projectionID, err, rollbackErr)
		}
		return fmt.Errorf("activate projection %q: persist ACTIVE failed and alias was rolled back: %w", projectionID, err)
	}

	cm.projectionMu.RLock()
	oldProjectionIDs := make([]string, 0)
	for id, candidate := range cm.projections {
		if id != projectionID && candidate.CollectionName == oldTarget && candidate.Status == string(capregistry.ProjectionActive) {
			oldProjectionIDs = append(oldProjectionIDs, id)
		}
	}
	cm.projectionMu.RUnlock()
	for _, id := range oldProjectionIDs {
		if err := cm.transitionProjection(ctx, id, capregistry.ProjectionRetired); err != nil {
			// The predecessor was not persisted as retired, so compensate
			// the alias and the new projection. If compensation fails, return
			// both facts: reconciliation is required and no false success is
			// reported.
			aliasErr := cm.restoreAlias(ctx, projection.AliasName, projection.CollectionName, oldTarget)
			if aliasErr == nil {
				stateErr := cm.transitionProjection(ctx, projectionID, capregistry.ProjectionRetired)
				if stateErr == nil {
					return fmt.Errorf("activate projection %q: retire predecessor %q failed; alias and state restored: %w", projectionID, id, err)
				}
				return fmt.Errorf("activate projection %q: retire predecessor %q failed; alias restored but new state compensation failed: %v; original: %w", projectionID, id, stateErr, err)
			}
			return fmt.Errorf("activate projection %q: retire predecessor %q failed; alias compensation failed: %v; original: %w", projectionID, id, aliasErr, err)
		}
	}
	if cm.OnAliasSwitch != nil {
		cm.OnAliasSwitch()
	}
	return nil
}

// Rollback is the concise canonical operation name.
func (cm *CollectionManager) Rollback(ctx context.Context, projectionID, rollbackTarget string) error {
	return cm.RollbackProjection(ctx, projectionID, rollbackTarget)
}

// MarkFailedCleaned transitions a FAILED projection to FAILED_CLEANED after
// its physical collection has been verified cleaned up. It fails closed
// unless: the projection exists and is FAILED, and the runtime alias does
// NOT point at its collection (the live write target is never marked
// cleaned). The attempt history is preserved — only the status changes.
func (cm *CollectionManager) MarkFailedCleaned(ctx context.Context, projectionID string) error {
	projection, err := cm.requireProjection(projectionID)
	if err != nil {
		return err
	}
	if capregistry.ProjectionStatus(projection.Status) != capregistry.ProjectionFailed {
		return fmt.Errorf("%w: mark failed-cleaned %q: status is %s, want FAILED", ErrProjectionInvalidState, projectionID, projection.Status)
	}
	target, err := cm.client.GetAliasTarget(ctx, projection.AliasName)
	if err != nil {
		var notFound *transport.ErrCollectionNotFound
		if !errors.As(err, &notFound) {
			return fmt.Errorf("mark failed-cleaned %q: resolve alias target: %w", projectionID, err)
		}
		// Alias unwritten: there is no target, so the collection is
		// definitely not the live write target.
	} else if target == projection.CollectionName {
		return fmt.Errorf("%w: mark failed-cleaned %q: collection %q is still the runtime alias target", ErrProjectionInvalidState, projectionID, projection.CollectionName)
	}
	return cm.transitionProjection(ctx, projectionID, capregistry.ProjectionFailedCleaned)
}

// RollbackProjection atomically restores the alias to rollbackTarget. A
// failed rollback leaves the currently active projection ACTIVE; a successful
// rollback retires the candidate and marks a known rollback build ACTIVE.
func (cm *CollectionManager) RollbackProjection(ctx context.Context, projectionID, rollbackTarget string) error {
	rollbackTarget = strings.TrimSpace(rollbackTarget)
	if rollbackTarget == "" {
		return ErrProjectionRollbackTarget
	}
	projection, err := cm.requireProjection(projectionID)
	if err != nil {
		return err
	}
	if projection.Status != string(capregistry.ProjectionActive) {
		return fmt.Errorf("%w: rollback requires ACTIVE projection, got %s", ErrProjectionInvalidState, projection.Status)
	}
	rollbackID, ok := cm.projectionByCollection(rollbackTarget)
	if !ok {
		return fmt.Errorf("%w: collection %q is not a known projection", ErrProjectionRollbackTarget, rollbackTarget)
	}
	rollbackProjection, _ := cm.Projection(rollbackID)
	if rollbackProjection.Status != string(capregistry.ProjectionRetired) {
		return fmt.Errorf("%w: collection %q has status %s, want RETIRED", ErrProjectionRollbackTarget, rollbackTarget, rollbackProjection.Status)
	}

	cm.aliasMu.Lock()
	defer cm.aliasMu.Unlock()
	activeTarget, err := cm.client.GetAliasTarget(ctx, projection.AliasName)
	if err != nil {
		return fmt.Errorf("resolve active projection before rollback: %w", err)
	}
	if activeTarget != projection.CollectionName {
		return fmt.Errorf("%w: alias %q points to %q, expected %q", ErrProjectionInvalidState, projection.AliasName, activeTarget, projection.CollectionName)
	}
	if err := cm.client.SwitchAlias(ctx, projection.AliasName, activeTarget, rollbackTarget); err != nil {
		return fmt.Errorf("rollback projection %q: %w", projectionID, err)
	}

	if err := cm.transitionProjection(ctx, projectionID, capregistry.ProjectionRetired); err != nil {
		rollbackErr := cm.restoreAlias(ctx, projection.AliasName, rollbackTarget, activeTarget)
		if rollbackErr != nil {
			return fmt.Errorf("rollback projection %q: persist RETIRED failed: %v; alias restore failed: %w", projectionID, err, rollbackErr)
		}
		return fmt.Errorf("rollback projection %q: persist RETIRED failed and alias was restored: %w", projectionID, err)
	}
	if err := cm.transitionProjection(ctx, rollbackID, capregistry.ProjectionActive); err != nil {
		aliasErr := cm.restoreAlias(ctx, projection.AliasName, rollbackTarget, activeTarget)
		stateErr := cm.transitionProjection(ctx, projectionID, capregistry.ProjectionActive)
		if aliasErr != nil || stateErr != nil {
			return fmt.Errorf("rollback projection %q: activate target %q failed: %v; alias restore=%v; active state restore=%v", projectionID, rollbackTarget, err, aliasErr, stateErr)
		}
		return fmt.Errorf("rollback projection %q: activate target %q failed and alias/state were restored: %w", projectionID, rollbackTarget, err)
	}
	if cm.OnAliasSwitch != nil {
		cm.OnAliasSwitch()
	}
	return nil
}

// ReconcileProjection compares the runtime alias with durable lifecycle
// metadata after restart. If Qdrant switched successfully but the process
// stopped before the ACTIVE registry write, a READY/RETIRED matching target
// is promoted in the registry without issuing another alias mutation. An
// unknown target or an impossible state fails closed for operator repair.
func (cm *CollectionManager) ReconcileProjection(ctx context.Context) error {
	cm.projectionMu.RLock()
	count := len(cm.projections)
	cm.projectionMu.RUnlock()
	if count == 0 {
		return nil
	}

	target, err := cm.client.GetAliasTarget(ctx, cm.schema.RuntimeAlias)
	if err != nil {
		var notFound *transport.ErrCollectionNotFound
		if errors.As(err, &notFound) {
			target = ""
		} else {
			return fmt.Errorf("reconcile projection alias: %w", err)
		}
	}
	if target == "" {
		return fmt.Errorf("%w: runtime alias %q has no target while projection state exists", ErrProjectionInvalidState, cm.schema.RuntimeAlias)
	}

	projectionID, ok := cm.projectionByCollection(target)
	if !ok {
		return fmt.Errorf("%w: runtime alias %q points to unknown collection %q", ErrProjectionInvalidState, cm.schema.RuntimeAlias, target)
	}
	projection, _ := cm.Projection(projectionID)
	latestSequence, err := cm.resolveRegistrySequence(ctx, projection.SourceRegistrySeq)
	if err != nil {
		return err
	}
	if err := capregistry.ValidateProjectionSequence(projection.SourceRegistrySeq, latestSequence); err != nil {
		if errors.Is(err, capregistry.ErrProjectionSequenceAhead) {
			// Qdrant is a rebuildable projection. A projection ahead of the
			// SQLite registry is drift, not a reason to stop unrelated
			// PipelineGen capabilities at startup. Keep the live alias and
			// let reconciliation/rebuild repair it asynchronously.
			cm.log.Warn("projection ahead of canonical registry; continuing startup in DIRTY mode",
				zap.String("projection_id", projectionID),
				zap.Int64("projection_seq", projection.SourceRegistrySeq),
				zap.Int64("registry_seq", latestSequence))
			return nil
		}
		return fmt.Errorf("reconcile projection %q: %w", projectionID, err)
	}
	info, err := cm.client.GetCollection(ctx, target)
	if err != nil {
		return fmt.Errorf("reconcile projection %q schema: %w", projectionID, err)
	}
	if diff := schema.CompareSchema(cm.schema, info); !diff.Compatible {
		return fmt.Errorf("%w: alias target %q schema is incompatible (missing_vectors=%v dimension_mismatches=%v missing_indexes=%v)", ErrProjectionInvalidState, target, diff.MissingVectors, diff.DimensionMismatches, diff.MissingIndexes)
	}
	switch capregistry.ProjectionStatus(projection.Status) {
	case capregistry.ProjectionActive:
		return nil
	case capregistry.ProjectionReady, capregistry.ProjectionRetired:
		if err := cm.transitionProjection(ctx, projectionID, capregistry.ProjectionActive); err != nil {
			return fmt.Errorf("reconcile projection %q as ACTIVE: %w", projectionID, err)
		}
		cm.projectionMu.RLock()
		predecessors := make([]string, 0)
		for id, candidate := range cm.projections {
			if id != projectionID && candidate.AliasName == projection.AliasName && candidate.Status == string(capregistry.ProjectionActive) {
				predecessors = append(predecessors, id)
			}
		}
		cm.projectionMu.RUnlock()
		for _, id := range predecessors {
			if err := cm.transitionProjection(ctx, id, capregistry.ProjectionRetired); err != nil {
				return fmt.Errorf("reconcile predecessor %q as RETIRED: %w", id, err)
			}
		}
		return nil
	default:
		return fmt.Errorf("%w: alias target %q has projection %q in state %s", ErrProjectionInvalidState, target, projectionID, projection.Status)
	}
}

func (cm *CollectionManager) projectionByCollection(collection string) (string, bool) {
	cm.projectionMu.RLock()
	defer cm.projectionMu.RUnlock()
	var found string
	matches := 0
	for id, projection := range cm.projections {
		if projection.CollectionName == collection {
			found = id
			matches++
		}
	}
	return found, matches == 1
}

func validateHydratedProjections(projections []capregistry.Projection, registrySequence int64) error {
	activeByAlias := make(map[string]string)
	for _, projection := range projections {
		if projection.ProjectionID == "" || projection.ProjectionType == "" || projection.CollectionName == "" || projection.AliasName == "" {
			return fmt.Errorf("hydrate projection registry: invalid projection identity %q", projection.ProjectionID)
		}
		switch capregistry.ProjectionStatus(projection.Status) {
		case capregistry.ProjectionBuilding, capregistry.ProjectionValidating,
			capregistry.ProjectionReady, capregistry.ProjectionActive,
			capregistry.ProjectionRetired, capregistry.ProjectionFailed,
			capregistry.ProjectionFailedCleaned:
		default:
			return fmt.Errorf("hydrate projection registry: unknown status %q for %q", projection.Status, projection.ProjectionID)
		}
		if projection.SourceRegistrySeq > registrySequence {
			return fmt.Errorf("hydrate projection registry: projection %q is ahead of canonical sequence: projection_seq=%d registry_seq=%d", projection.ProjectionID, projection.SourceRegistrySeq, registrySequence)
		}
		if projection.Status == string(capregistry.ProjectionActive) {
			if previous, exists := activeByAlias[projection.AliasName]; exists {
				return fmt.Errorf("hydrate projection registry: multiple ACTIVE projections for alias %q: %q and %q", projection.AliasName, previous, projection.ProjectionID)
			}
			activeByAlias[projection.AliasName] = projection.ProjectionID
		}
	}
	return nil
}

func (cm *CollectionManager) restoreAlias(ctx context.Context, alias, currentTarget, previousTarget string) error {
	if previousTarget == "" {
		return cm.client.DeleteAlias(ctx, alias)
	}
	return cm.client.SwitchAlias(ctx, alias, currentTarget, previousTarget)
}

func (cm *CollectionManager) requireProjection(projectionID string) (capregistry.Projection, error) {
	cm.projectionMu.RLock()
	projection, ok := cm.projections[projectionID]
	cm.projectionMu.RUnlock()
	if !ok {
		return capregistry.Projection{}, fmt.Errorf("%w: %q", ErrProjectionNotFound, projectionID)
	}
	return projection, nil
}

func (cm *CollectionManager) transitionProjection(ctx context.Context, projectionID string, next capregistry.ProjectionStatus) error {
	cm.projectionMu.Lock()
	defer cm.projectionMu.Unlock()
	projection, ok := cm.projections[projectionID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrProjectionNotFound, projectionID)
	}
	current := capregistry.ProjectionStatus(projection.Status)
	if err := capregistry.ValidateProjectionTransition(current, next); err != nil {
		return err
	}
	previous := projection
	projection.Status = string(next)
	if next == capregistry.ProjectionActive {
		projection.ActivatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	cm.projections[projectionID] = projection
	if cm.registryLedger != nil {
		if err := cm.registryLedger.RegisterProjection(ctx, projection); err != nil {
			cm.projections[projectionID] = previous
			return fmt.Errorf("persist projection %q as %s: %w", projectionID, next, err)
		}
	}
	return nil
}

func (cm *CollectionManager) failProjection(ctx context.Context, projectionID string) error {
	cm.projectionMu.Lock()
	defer cm.projectionMu.Unlock()
	projection, ok := cm.projections[projectionID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrProjectionNotFound, projectionID)
	}
	if projection.Status == string(capregistry.ProjectionFailed) {
		return nil
	}
	if err := capregistry.ValidateProjectionTransition(capregistry.ProjectionStatus(projection.Status), capregistry.ProjectionFailed); err != nil {
		return err
	}
	previous := projection
	projection.Status = string(capregistry.ProjectionFailed)
	cm.projections[projectionID] = projection
	if cm.registryLedger != nil {
		if err := cm.registryLedger.RegisterProjection(ctx, projection); err != nil {
			cm.projections[projectionID] = previous
			return err
		}
	}
	return nil
}

func (cm *CollectionManager) resolveRegistrySequence(ctx context.Context, requested int64) (int64, error) {
	cm.projectionMu.RLock()
	ledger := cm.registryLedger
	cm.projectionMu.RUnlock()
	if ledger == nil {
		return requested, nil
	}
	sequenceReader, ok := ledger.(capregistry.QdrantSequenceReader)
	if ok {
		sequence, err := sequenceReader.LatestQdrantEventSequence(ctx)
		if err != nil {
			return 0, fmt.Errorf("read canonical qdrant registry sequence: %w", err)
		}
		return sequence, nil
	}
	sequence, err := ledger.LatestEventSequence(ctx)
	if err != nil {
		return 0, fmt.Errorf("read canonical registry sequence: %w", err)
	}
	return sequence, nil
}
