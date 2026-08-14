package collections

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
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

	result := &RetentionResult{DryRun: cfg.DryRun}
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

	// PR-RETENTION-RETIRED-SCHEMAS: match the current schema prefix plus
	// any explicitly-supplied retired-generation prefixes (e.g. the
	// superseded e5 schema). A retired prefix that overlaps the current
	// canonical name is rejected fail-closed.
	prefixes, err := retentionPrefixes(cm.schema.CanonicalName(), cfg.RetiredPrefixes)
	if err != nil {
		return nil, err
	}

	statuses, err := cm.projectionStatuses(ctx)
	if err != nil {
		return nil, err
	}

	// Eligible: matches any prefix + NOT the active target + NOT marked
	// ACTIVE in the projection registry (defense-in-depth: the registry
	// is the durable lifecycle truth; a collection marked ACTIVE is never
	// dropped even if the alias momentarily disagrees).
	eligible := make([]string, 0, len(names))
	for _, name := range names {
		if !matchesAnyPrefix(name, prefixes) {
			continue
		}
		if name == activeTarget {
			result.CollectionsKept++
			continue
		}
		if statuses[name] == mediaregistry.ProjectionActive {
			result.CollectionsKept++
			result.ProtectedKept = append(result.ProtectedKept, name)
			cm.log.Warn("retention sweep: protecting registry-ACTIVE collection that is not the alias target",
				zap.String("name", name))
			continue
		}
		eligible = append(eligible, name)
	}

	// Stale partials (FAILED / BUILDING / VALIDATING) are never protected
	// by the keep_last_n tail — a failed build must not crowd out a
	// known-good rollback target just because its timestamp is newer.
	tailCandidates := make([]string, 0, len(eligible))
	for _, name := range eligible {
		if isStalePartialStatus(statuses[name]) {
			continue
		}
		tailCandidates = append(tailCandidates, name)
	}

	// Safe-keep: the protected rollback target is always pinned.
	keepSet := make(map[string]bool)
	if cfg.ProtectedRollbackTarget != "" {
		keepSet[cfg.ProtectedRollbackTarget] = true
	}

	// PR 9 (#14): sort descending (newest-first), keep keepLastN-1 known-good,
	// drop the rest.
	sort.Sort(sort.Reverse(sort.StringSlice(tailCandidates)))
	keepLeft := keepLastN - 1
	for _, name := range tailCandidates {
		if keepLeft <= 0 {
			break
		}
		if keepSet[name] {
			continue
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
			zap.Int("keep_last_n", keepLastN))
	}
	return result, nil
}

// retentionPrefixes returns the current schema prefix plus any retired
// prefixes, deduplicated and validated. A retired prefix that equals — or
// is a prefix of — the current canonical name is rejected fail-closed:
// such a prefix would match the live collection fleet (e.g. a bare
// "media_assets" or "media_assets_v3").
func retentionPrefixes(current string, retired []string) ([]string, error) {
	prefixes := make([]string, 0, 1+len(retired))
	seen := map[string]bool{current: true}
	prefixes = append(prefixes, current)
	for _, p := range retired {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if p == current || strings.HasPrefix(current, p) {
			return nil, fmt.Errorf("retention: retired prefix %q overlaps the current schema prefix %q and would match live collections", p, current)
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		prefixes = append(prefixes, p)
	}
	return prefixes, nil
}

// matchesAnyPrefix reports whether name has any of the given prefixes.
func matchesAnyPrefix(name string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
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

// isStalePartialStatus reports whether a projection status represents a
// build that never became a known-good target. Such collections must never
// be protected by the keep_last_n tail (the operator must not run retention
// concurrently with an in-flight reindex).
func isStalePartialStatus(status mediaregistry.ProjectionStatus) bool {
	switch status {
	case mediaregistry.ProjectionFailed, mediaregistry.ProjectionBuilding, mediaregistry.ProjectionValidating:
		return true
	default:
		return false
	}
}
