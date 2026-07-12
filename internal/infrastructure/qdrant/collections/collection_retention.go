package collections

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"go.uber.org/zap"

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
func (cm *CollectionManager) CleanupWithConfig(ctx context.Context, cfg RetentionConfig) (*RetentionResult, error) {
	if cfg.RetentionDays <= 0 {
		return &RetentionResult{}, nil
	}
	keepLastN := cfg.KeepLastN
	if keepLastN < 2 {
		keepLastN = 2
	}

	result := &RetentionResult{}
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
	prefix := cm.schema.CanonicalName()

	// Eligible: matching prefix + NOT active.
	eligible := make([]string, 0, len(names))
	for _, name := range names {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		if name == activeTarget {
			result.CollectionsKept++
			continue
		}
		eligible = append(eligible, name)
	}

	// Safe-keep: protected rollback target is always pinned.
	keepSet := make(map[string]bool)
	if cfg.ProtectedRollbackTarget != "" {
		keepSet[cfg.ProtectedRollbackTarget] = true
	}

	// PR 9 (#14): sort descending (newest-first), keep keepLastN-1,
	// drop the rest.
	sort.Sort(sort.Reverse(sort.StringSlice(eligible)))
	keepLeft := keepLastN - 1
	for _, name := range eligible {
		if keepLeft <= 0 {
			break
		}
		keepSet[name] = true
		keepLeft--
		result.CollectionsKept++
		result.ProtectedKept = append(result.ProtectedKept, name)
	}

	for _, name := range eligible {
		if keepSet[name] {
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
			zap.Int("keep_last_n", keepLastN))
	}
	return result, nil
}
