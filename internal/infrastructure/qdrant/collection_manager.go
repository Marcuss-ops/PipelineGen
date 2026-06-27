package qdrant

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/qdrantdr"
)

// CollectionManager handles Qdrant collection lifecycle: inspection, creation,
// schema enforcement, alias management, and migration orchestration.
type CollectionManager struct {
	client *Client
	schema *IndexSchema
	log    *zap.Logger
}

// NewCollectionManager creates a CollectionManager bound to a schema.
func NewCollectionManager(client *Client, schema *IndexSchema, log *zap.Logger) *CollectionManager {
	return &CollectionManager{
		client: client,
		schema: schema,
		log:    log,
	}
}

// EnsureSchema guarantees the runtime alias points to a compatible collection.
// If no compatible collection exists, a new physical collection is created.
// The alias is NOT automatically switched — call SwitchAlias explicitly after
// reindex to promote a new collection into service.
//
// Policy:
//   - If the alias points to a compatible collection → success.
//   - If the alias is missing but a compatible collection exists → create alias.
//   - If schema is incompatible → do NOT mutate the existing collection.
//   - Create a new physical collection and return its name.
func (cm *CollectionManager) EnsureSchema(ctx context.Context) (*EnsureResult, error) {
	if err := cm.schema.Validate(); err != nil {
		return nil, fmt.Errorf("invalid schema: %w", err)
	}

	physName := cm.schema.physicalName()

	// 1. Check if the alias already points to a compatible collection.
	target, err := cm.client.GetAliasTarget(ctx, cm.schema.RuntimeAlias)
	if err != nil {
		if _, ok := err.(*ErrCollectionNotFound); !ok {
			return nil, fmt.Errorf("get alias target %q: %w", cm.schema.RuntimeAlias, err)
		}
		// Alias doesn't exist yet — fall through.
	}
	if target != "" {
		info, err := cm.client.GetCollection(ctx, target)
		if err != nil {
			return nil, fmt.Errorf("get aliased collection %q: %w", target, err)
		}
		diff := CompareSchema(cm.schema, info)
		if diff.Compatible {
			cm.log.Info("alias points to compatible collection",
				zap.String("alias", cm.schema.RuntimeAlias),
				zap.String("target", target))
			return &EnsureResult{
				Collection:  target,
				AliasTarget: target,
				Compatible:  true,
				Created:     false,
			}, nil
		}
		cm.log.Warn("alias target is incompatible, will create new collection",
			zap.String("alias", cm.schema.RuntimeAlias),
			zap.String("target", target),
			zap.Any("diff", diff))
	}

	// 2. Check if the physical collection already exists.
	info, err := cm.client.GetCollection(ctx, physName)
	if err == nil {
		diff := CompareSchema(cm.schema, info)
		if diff.Compatible {
			cm.log.Info("physical collection exists and is compatible",
				zap.String("collection", physName))
			// Create alias if missing.
			if target == "" {
				if err := cm.client.CreateAlias(ctx, cm.schema.RuntimeAlias, physName); err != nil {
					return nil, fmt.Errorf("create alias %q -> %q: %w", cm.schema.RuntimeAlias, physName, err)
				}
				cm.log.Info("created alias",
					zap.String("alias", cm.schema.RuntimeAlias),
					zap.String("target", physName))
				return &EnsureResult{
					Collection:   physName,
					AliasTarget:  physName,
					Compatible:   true,
					Created:      false,
					AliasCreated: true,
				}, nil
			}
			return &EnsureResult{
				Collection:  physName,
				AliasTarget: target,
				Compatible:  true,
				Created:     false,
			}, nil
		}
		return nil, &ErrSchemaIncompatible{Diff: diff}
	}
	if _, ok := err.(*ErrCollectionNotFound); !ok {
		return nil, fmt.Errorf("get collection %q: %w", physName, err)
	}

	// 3. Create the new physical collection.
	if err := cm.createPhysicalCollection(ctx, physName); err != nil {
		return nil, fmt.Errorf("create collection %q: %w", physName, err)
	}

	// 4. Create payload indexes.
	if err := cm.createPayloadIndexes(ctx, physName); err != nil {
		return nil, fmt.Errorf("create payload indexes for %q: %w", physName, err)
	}

	// 5. If no alias exists yet, create it pointing to the new collection.
	if target == "" {
		if err := cm.client.CreateAlias(ctx, cm.schema.RuntimeAlias, physName); err != nil {
			return nil, fmt.Errorf("create alias %q -> %q: %w", cm.schema.RuntimeAlias, physName, err)
		}
		cm.log.Info("created alias for new collection",
			zap.String("alias", cm.schema.RuntimeAlias),
			zap.String("target", physName))
	}

	cm.log.Info("created new physical collection",
		zap.String("collection", physName),
		zap.String("version", cm.schema.Version))

	return &EnsureResult{
		Collection:   physName,
		AliasTarget:  physName,
		Compatible:   true,
		Created:      true,
		AliasCreated: target == "",
	}, nil
}

// GetActiveCollection returns the collection currently pointed to by the runtime alias.
func (cm *CollectionManager) GetActiveCollection(ctx context.Context) (string, error) {
	return cm.client.GetAliasTarget(ctx, cm.schema.RuntimeAlias)
}

// InspectCollection returns detailed info about a collection.
func (cm *CollectionManager) InspectCollection(ctx context.Context, name string) (*CollectionInfo, error) {
	return cm.client.GetCollection(ctx, name)
}

// CompareActiveCollection compares the active (aliased) collection against the
// expected schema and returns the diff. Returns nil error only if the collection
// is accessible; check diff.Compatible for schema compatibility.
func (cm *CollectionManager) CompareActiveCollection(ctx context.Context) (*SchemaDiff, error) {
	target, err := cm.client.GetAliasTarget(ctx, cm.schema.RuntimeAlias)
	if err != nil {
		return nil, fmt.Errorf("get alias target: %w", err)
	}
	if target == "" {
		return nil, fmt.Errorf("alias %q has no target", cm.schema.RuntimeAlias)
	}
	info, err := cm.client.GetCollection(ctx, target)
	if err != nil {
		return nil, fmt.Errorf("get collection %q: %w", target, err)
	}
	return CompareSchema(cm.schema, info), nil
}

// SwitchAlias atomically switches the runtime alias from oldTarget to newTarget.
// This is the canonical switch after reindex verification.
func (cm *CollectionManager) SwitchAlias(ctx context.Context, oldTarget, newTarget string) error {
	cm.log.Info("switching alias",
		zap.String("alias", cm.schema.RuntimeAlias),
		zap.String("old", oldTarget),
		zap.String("new", newTarget))
	return cm.client.SwitchAlias(ctx, cm.schema.RuntimeAlias, oldTarget, newTarget)
}

// RollbackAlias switches the alias back to oldTarget.
func (cm *CollectionManager) RollbackAlias(ctx context.Context, currentTarget, rollbackTarget string) error {
	cm.log.Warn("rolling back alias",
		zap.String("alias", cm.schema.RuntimeAlias),
		zap.String("from", currentTarget),
		zap.String("to", rollbackTarget))
	return cm.client.SwitchAlias(ctx, cm.schema.RuntimeAlias, currentTarget, rollbackTarget)
}

// ── Snapshot / Restore (QDRANT-005, June 2026) ───────────────────────

func (cm *CollectionManager) CreateSnapshot(ctx context.Context, collection string) (*SnapshotDescription, error) {
	cm.log.Info("creating snapshot", zap.String("collection", collection))
	snap, err := cm.client.CreateSnapshot(ctx, collection)
	if err != nil { return nil, fmt.Errorf("create snapshot %q: %w", collection, err) }
	cm.log.Info("snapshot created", zap.String("collection", collection), zap.String("name", snap.Name), zap.Int64("size", snap.Size))
	return snap, nil
}

func (cm *CollectionManager) ListSnapshots(ctx context.Context) ([]SnapshotDescription, error) {
	target, err := cm.client.GetAliasTarget(ctx, cm.schema.RuntimeAlias)
	if err != nil { return nil, fmt.Errorf("get active collection: %w", err) }
	if target == "" { return nil, fmt.Errorf("no active collection for alias %q", cm.schema.RuntimeAlias) }
	return cm.client.ListSnapshots(ctx, target)
}

func (cm *CollectionManager) RestoreSnapshot(ctx context.Context, collection, snapshotURL string) error {
	cm.log.Warn("restoring collection from snapshot", zap.String("collection", collection), zap.String("snapshot_url", snapshotURL))
	return cm.client.RestoreSnapshot(ctx, collection, snapshotURL)
}

// ── Retention (QDRANT-005, June 2026) ────────────────────────────────

// RetentionConfig is the canonical DR retention config (type alias).
// See internal/domain/qdrantdr/types.go for the canonical definition.
// AgingTable is the infra-side collection-aging interface; the alias
// field is typed `any` in the domain package. Callers that pass an
// AgingTable must type-assert cfg.AgingTable.(AgingTable) at call sites.
type RetentionConfig = qdrantdr.RetentionConfig

// RetentionResult is the canonical DR retention result (type alias).
type RetentionResult = qdrantdr.RetentionResult

// AgingTable is the optional interface for the Qdrant collection
// aging registry. The canonical implementation lives in
// internal/infrastructure/database/sqlite/qdrantcolls/ (QDRANT-005
// follow-up); today this is accepted but unused — see RetentionConfig
// for the graduated ramp plan.
type AgingTable interface {
	CreatedAt(ctx context.Context, collection string) (string, bool, error)
}

// CleanupOldCollections drops all non-active collections whose names
// share the schema's physical-name prefix. The retentionDays
// parameter remains the policy SWITCH (≤0 = no-op) for backward
// compatibility with the previous signature; the actual retention
// semantics are governed by RetentionConfig.
//
// QDRANT-005 closure (June 2026): the previous implementation was
// destructive — `retentionDays > 0` deleted EVERY non-active
// collection regardless of age and without preserving a rollback
// target. The new path:
//
//  1. Resolves the active alias target (kept — it stays in service).
//  2. Identifies all collections matching the schema prefix.
//  3. Computes a SAFE-KEEP set = {active, protected_rollback_target}
//     ∪ (last N collections by name,KeepLastN preferred, defaulted 2).
//  4. Drops the rest.
//
// The keep_last_n floor guarantees the operator always has at least
// two collections reachable: the active one and one rollback target.
func (cm *CollectionManager) CleanupOldCollections(ctx context.Context, retentionDays int) (*RetentionResult, error) {
	return cm.CleanupWithConfig(ctx, RetentionConfig{
		RetentionDays: retentionDays,
		KeepLastN:     2,
	})
}

// CleanupWithConfig runs the retention sweep with an explicit
// RetentionConfig (preferred entry point for new callers; the
// retentionDays-only call site stays for back-compat).
//
// QDRANT-005 closure (June 2026): keep_last_n default = 2 (one
// active + one rollback); protected_rollback_target is never dropped;
// the rest are dropped without age gating (Qdrant REST has no
// per-collection timestamp API until the SQLite aging registry
// migration lands).
func (cm *CollectionManager) CleanupWithConfig(ctx context.Context, cfg RetentionConfig) (*RetentionResult, error) {
	if cfg.RetentionDays <= 0 {
		return &RetentionResult{}, nil
	}
	keepLastN := cfg.KeepLastN
	if keepLastN < 2 {
		keepLastN = 2 // hard floor — at least active + one rollback
	}

	result := &RetentionResult{}
	names, err := cm.client.ListCollections(ctx)
	if err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}
	activeTarget, _ := cm.client.GetAliasTarget(ctx, cm.schema.RuntimeAlias)
	prefix := cm.schema.physicalName()

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

	// Sort eligible newest-first by name (each new reindex produces
	// a name with a higher suffix — e.g. media_assets_v3__ts_202602...).
	// keep_last_n tail stays untouched.
	sort.Strings(eligible)
	keepLeft := keepLastN - 1 // active already counted
	for _, name := range eligible {
		if keepLeft > 0 {
			keepSet[name] = true
			keepLeft--
			result.CollectionsKept++
			result.ProtectedKept = append(result.ProtectedKept, name)
			continue
		}
		break
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


// createPhysicalCollection creates the collection with vector config from the manifest.
func (cm *CollectionManager) createPhysicalCollection(ctx context.Context, name string) error {
	vectors := make(map[string]interface{})
	sparseVectors := make(map[string]interface{})

	for _, v := range cm.schema.DenseVectors {
		vectors[v.Channel] = map[string]interface{}{
			"size":     v.Dimensions,
			"distance": v.Distance,
		}
	}
	for _, v := range cm.schema.SparseVectors {
		sv := map[string]interface{}{}
		if v.Modifier != "" {
			sv["modifier"] = v.Modifier
		}
		sparseVectors[v.Channel] = sv
	}

	return cm.client.CreateCollection(ctx, name, vectors, sparseVectors)
}

// createPayloadIndexes creates all payload indexes from the manifest.
func (cm *CollectionManager) createPayloadIndexes(ctx context.Context, collection string) error {
	for _, idx := range cm.schema.PayloadIndexes {
		if err := cm.client.CreatePayloadIndex(ctx, collection, idx.FieldName, idx.FieldType); err != nil {
			cm.log.Warn("failed to create payload index",
				zap.String("collection", collection),
				zap.String("field", idx.FieldName),
				zap.String("type", idx.FieldType),
				zap.Error(err))
			return fmt.Errorf("payload index %q: %w", idx.FieldName, err)
		}
	}
	return nil
}

// ── Types ────────────────────────────────────────────────────────────

// CreateCollection creates a new physical collection with the schema's
// vector config and payload indexes, then returns nil. Used by the admin
// reindex command (QDRANT-003 immutable-collection pattern) to create a
// timestamped target before ReindexAll, so the reindex never writes into
// the active (aliased) collection.
func (cm *CollectionManager) CreateCollection(ctx context.Context, name string) error {
	if err := cm.createPhysicalCollection(ctx, name); err != nil {
		return fmt.Errorf("create physical collection %q: %w", name, err)
	}
	if err := cm.createPayloadIndexes(ctx, name); err != nil {
		return fmt.Errorf("create payload indexes for %q: %w", name, err)
	}
	return nil
}

// EnsureResult is the outcome of EnsureSchema.
type EnsureResult struct {
	Collection   string `json:"collection"`
	AliasTarget  string `json:"alias_target"`
	Compatible   bool   `json:"compatible"`
	Created      bool   `json:"created"`
	AliasCreated bool   `json:"alias_created"`
}
