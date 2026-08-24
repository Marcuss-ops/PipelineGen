package collections

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
)

// ── Prepare ────────────────────────────────────────────────────────────

// InspectRuntime returns the current alias target and the
// schema.SchemaDiff against cm.schema. target == "" means the alias is
// unwritten (transport.ErrCollectionNotFound from the client is swallowed).
// diff is nil only if target == "".
func (cm *CollectionManager) InspectRuntime(ctx context.Context) (target string, diff *schema.SchemaDiff, err error) {
	target, err = cm.client.GetAliasTarget(ctx, cm.schema.RuntimeAlias)
	if err != nil {
		if _, ok := err.(*transport.ErrCollectionNotFound); ok {
			return "", nil, nil
		}
		return "", nil, fmt.Errorf("get alias target %q: %w", cm.schema.RuntimeAlias, err)
	}
	if target == "" {
		return "", nil, nil
	}
	info, err := cm.client.GetCollection(ctx, target)
	if err != nil {
		return "", nil, fmt.Errorf("get aliased collection %q: %w", target, err)
	}
	diff = schema.CompareSchema(cm.schema, info)
	return target, diff, nil
}

// PrepareCandidate creates the physical collection + payload indexes
// for `candidate`. NO alias is written. Resets verifyLedger[candidate]
// so a re-prepare invalidates any prior verification.
func (cm *CollectionManager) PrepareCandidate(ctx context.Context, candidate string) error {
	cm.resetVerified(candidate)
	if err := cm.createPhysicalCollection(ctx, candidate); err != nil {
		return fmt.Errorf("create physical %q: %w", candidate, err)
	}
	if err := cm.createPayloadIndexes(ctx, candidate); err != nil {
		return fmt.Errorf("create payload indexes for %q: %w", candidate, err)
	}
	return nil
}

// createPhysicalCollection creates the collection with vector config from the manifest.
func (cm *CollectionManager) createPhysicalCollection(ctx context.Context, name string) error {
	vectors := make(map[string]any)
	sparseVectors := make(map[string]any)

	for _, v := range cm.schema.DenseVectors {
		vectors[v.Channel] = map[string]any{
			"size":     v.Dimensions,
			"distance": v.Distance,
		}
	}
	for _, v := range cm.schema.SparseVectors {
		sv := map[string]any{}
		if v.Modifier != "" {
			sv["modifier"] = v.Modifier
		}
		model := v.Model
		if model == "" {
			model = schema.DefaultSparseModel
		}
		sv["model"] = model
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
