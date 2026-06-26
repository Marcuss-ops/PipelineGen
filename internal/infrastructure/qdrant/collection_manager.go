package qdrant

import (
	"context"
	"fmt"

	"go.uber.org/zap"
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

// EnsureResult is the outcome of EnsureSchema.
type EnsureResult struct {
	Collection   string `json:"collection"`
	AliasTarget  string `json:"alias_target"`
	Compatible   bool   `json:"compatible"`
	Created      bool   `json:"created"`
	AliasCreated bool   `json:"alias_created"`
}
