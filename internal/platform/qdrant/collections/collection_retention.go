package collections

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/projectionretention"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/qdrantdr"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
)

// ── Retention ──────────────────────────────────────────────────────────

// RetentionConfig is the canonical DR retention config (type alias).
type RetentionConfig = qdrantdr.RetentionConfig

// RetentionResult is the canonical DR retention result (type alias).
type RetentionResult = qdrantdr.RetentionResult

// AgingTable is the optional interface for the Qdrant collection
// aging registry.
type AgingTable interface {
	CreatedAt(ctx context.Context, collection string) (string, bool, error)
}

// RetireOldCollections drops all non-active collections whose names
// share the schema's physical-name prefix.
func (cm *CollectionManager) RetireOldCollections(ctx context.Context, cfg RetentionConfig) (*RetentionResult, error) {
	return cm.CleanupWithConfig(ctx, cfg)
}

// CleanupOldCollections drops all non-active collections whose names
// share the schema's physical-name prefix. The retentionDays
// parameter remains the policy SWITCH (≤0 = no-op).
func (cm *CollectionManager) CleanupOldCollections(ctx context.Context, retentionDays int) (*RetentionResult, error) {
	return cm.CleanupWithConfig(ctx, RetentionConfig{
		RetentionDays: retentionDays,
		KeepLastN:     2,
	})
}

// CleanupWithConfig runs the retention sweep with an explicit
// RetentionConfig (preferred entry point for new callers).
//
// QDRANT-005 closure (June 2026): keep_last_n default = 2 (one
// active + one rollback); protected_rollback_target is never dropped.
//
// The drop/keep DECISION is delegated to the canonical
// capabilities/projectionretention policy (single source of truth); this
// adapter only performs the I/O (list collections, resolve alias, read
// statuses, delete).
func (cm *CollectionManager) CleanupWithConfig(ctx context.Context, cfg RetentionConfig) (*RetentionResult, error) {
	if cfg.RetentionDays <= 0 {
		return &RetentionResult{}, nil
	}

	names, err := cm.client.ListCollections(ctx)
	if err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}
	// Fail-closed resolution of the active alias. The only acceptable
	// non-success is `*transport.ErrCollectionNotFound` (the alias is
	// genuinely unwritten — expected on fresh bootstrap; the sweep
	// proceeds with activeTarget == ""). Any other error (5xx, timeout,
	// malformed body, OOM-empty response) means the active collection
	// is alive in qdrant but invisible to us, and proceeding would
	// risk dropping the production write target. AGENTS.md mandate:
	// "Fail closed with typed errors. Never represent an unavailable
	// backend as a successful no-op." Mirrors InspectRuntime at
	// collection_prepare.go:20-25.
	activeTarget, err := cm.client.GetAliasTarget(ctx, cm.schema.RuntimeAlias)
	if err != nil {
		var notFound *transport.ErrCollectionNotFound
		if errors.As(err, &notFound) {
			cm.log.Warn("retention sweep: alias unwritten, treating active target as empty",
				zap.String("alias", cm.schema.RuntimeAlias))
		} else {
			cm.log.Error("retention sweep: failed to resolve active alias (failing closed to prevent data loss)",
				zap.Error(err))
			return nil, fmt.Errorf("resolve active target: %w", err)
		}
	}

	statuses, err := cm.projectionStatuses(ctx)
	if err != nil {
		return nil, err
	}

	plan, err := (projectionretention.ProjectionRetentionPolicy{
		KeepLastN:       cfg.KeepLastN,
		RetentionDays:   cfg.RetentionDays,
		RetiredPrefixes: cfg.RetiredPrefixes,
	}).Decide(projectionretention.Input{
		Collections:       names,
		ActiveTarget:      activeTarget,
		CurrentPrefix:     cm.schema.CanonicalName(),
		Statuses:          statuses,
		ProtectedRollback: cfg.ProtectedRollbackTarget,
	})
	if err != nil {
		return nil, err
	}

	result := &RetentionResult{
		DryRun:          cfg.DryRun,
		CollectionsKept: len(plan.Keep),
		ProtectedKept:   plan.Protected,
	}
	for _, name := range plan.Drop {
		if cfg.DryRun {
			result.CollectionsDropped++
			result.DroppedNames = append(result.DroppedNames, name)
			continue
		}
		if err := cm.client.DeleteCollection(ctx, name); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("drop %s: %v", name, err))
			continue
		}
		result.CollectionsDropped++
		result.DroppedNames = append(result.DroppedNames, name)
		cm.log.Info("retention: dropped old collection",
			zap.String("name", name),
			zap.Int("retention_days", cfg.RetentionDays),
			zap.Int("keep_last_n", maxInt(cfg.KeepLastN, 2)))
	}
	return result, nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// projectionStatuses reads the durable projection lifecycle statuses from
// the wired registry ledger (via ListProjections). Returns nil when no
// ledger is wired, in which case the sweep falls back to the legacy
// status-less keep_last_n behaviour. A read error fails closed — without
// status the sweep cannot distinguish a known-good rollback from a failed
// partial and could delete the wrong collection.
func (cm *CollectionManager) projectionStatuses(ctx context.Context) (map[string]mediaregistry.ProjectionStatus, error) {
	cm.projectionMu.RLock()
	ledger := cm.registryLedger
	cm.projectionMu.RUnlock()
	if ledger == nil {
		return nil, nil
	}
	reader, ok := ledger.(mediaregistry.ProjectionReader)
	if !ok {
		return nil, nil
	}
	projections, err := reader.ListProjections(ctx)
	if err != nil {
		return nil, fmt.Errorf("list projections for retention: %w", err)
	}
	statuses := make(map[string]mediaregistry.ProjectionStatus, len(projections))
	for _, projection := range projections {
		statuses[projection.CollectionName] = mediaregistry.ProjectionStatus(projection.Status)
	}
	return statuses, nil
}
