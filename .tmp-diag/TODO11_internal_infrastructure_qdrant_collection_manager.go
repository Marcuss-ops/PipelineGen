package qdrant

import (
	"context"
	"errors"
	"fmt"
	"time"

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

// ── Blue-green reindex helpers (QDRANT-007 TODO 11, June 2026) ─

// ErrTimestampCollision is the canonical sentinel for the
// QDRANT-007 TODO 11 collision contract: after EnsureUniqueName
// regenerates the timestamp exactly once and the regenerated name is
// STILL in use, the second collision is surfaced as an explicit
// refusal rather than looping. The error message carries enough
// detail for operator triage (sustained clock skew, parallel
// reindex runs, or a race with a previous un-cleaned-up
// timestamped collection). Tests assert
// `errors.Is(err, ErrTimestampCollision)`; callers surface it as a
// distinct UI signal (operator message + suggested workaround).
//
// WHY ONE RETRY: the spec mandates "rigenera una volta, poi fail"
// (regenerate once, then fail). The first retry absorbs the
// overwhelmingly common case of a clock-tick boundary between
// GenerateTimestampName calls in this process; a second collision
// is rare enough (microsecond-level clock resolution in Qdrant,
// ±couple of seconds of skew between runs) that looping is the
// wrong move — every additional retry wastes an HTTP round-trip on
// an exception case that should be investigated, not silenced.
var ErrTimestampCollision = errors.New(
	"qdrant: timestamp collision after regenerate — blue-green collection " +
		"name uniqueness could not be guaranteed in a single retry " +
		"(sustained clock skew or parallel reindex run suspected)",
)

// GenerateTimestampName (QDRANT-007 TODO 11, June 2026) produces a
// unique blue-green reindex collection name. The spec-literal
// format is
//
//	media_assets_<schema_version>_<UTC timestamp>
//
// e.g. `media_assets_v3_20260627_153045` for a v3 reindex on
// 27 June 2026 at 15:30:45 UTC. The schema version is the
// canonical `IndexSchema.Version` (NOT `IndexSchema.PhysicalName`)
// so callers do NOT couple to the model-encoding suffix baked into
// physical names like `media_assets_v3_e5_768_siglip_768`. The
// timestamp uses `time.Now().UTC().Format("20060102_150405")`
// per spec — UTC so two reindexes in different timezones never
// share a single timestamp collision; the underscore-separated
// shape is the canonical Qdrant collection-name tokeniser.
//
// This function is a PURE HELPER — it does NOT check whether the
// returned name exists in Qdrant. Use CollectionManager.EnsureUniqueName
// to handle the rare timestamp-collision case (the second-collide
// sentinel is the only failure mode).
func GenerateTimestampName(schemaVersion string) string {
	return fmt.Sprintf(
		"media_assets_%s_%s",
		schemaVersion,
		time.Now().UTC().Format("20060102_150405"),
	)
}

// collectionExists reports whether the named Qdrant collection
// exists. Internally realised via the canonical GetCollection
// handler: a `*ErrCollectionNotFound` return collapses to (false,
// nil); any other error propagates. Used by EnsureUniqueName's
// collision-detection path.
func (cm *CollectionManager) collectionExists(ctx context.Context, name string) (bool, error) {
	info, err := cm.client.GetCollection(ctx, name)
	if err == nil {
		_ = info // existence confirmed; full CollectionInfo is not consumed downstream
		return true, nil
	}
	if _, ok := err.(*ErrCollectionNotFound); ok {
		return false, nil
	}
	return false, fmt.Errorf("check collection %q: %w", name, err)
}

// EnsureUniqueName implements the QDRANT-007 TODO 11 one-retry
// collision contract:
//
//   - proposed is free in Qdrant                     → return (proposed, nil)
//   - proposed is in use, regenerated is free        → return (regenerated, nil)
//   - proposed is in use, regenerated is ALSO in use → return (regenerated, ErrTimestampCollision)
//   - either call fails for a non-NotFound reason    → return ("", wrapped error)
//
// `regenerated` is produced via `GenerateTimestampName(cm.schema.Version)`;
// when the regeneration produces the same string as `proposed`
// (the in-process clock has not advanced a second between the two
// calls), the nanosecond-clocked suffix is appended so the second
// call still produces a distinct name. Without that guard an
// in-process retry within the same second would deterministically
// collide and mask the rare parallel-run case the specs ask
// operators to triage.
//
// Callers MUST NOT invoke GenerateTimestampName themselves before
// calling EnsureUniqueName — the collision-retry logic needs to
// own the regeneration, otherwise callers produce a name that
// already collides with their own first guess and only start the
// retry loop on the third attempt.
func (cm *CollectionManager) EnsureUniqueName(ctx context.Context, proposed string) (string, error) {
	exists, err := cm.collectionExists(ctx, proposed)
	if err != nil {
		return "", fmt.Errorf("check proposed name %q: %w", proposed, err)
	}
	if !exists {
		return proposed, nil
	}

	regen := GenerateTimestampName(cm.schema.Version)
	if regen == proposed {
		// In-process same-second retry: append nanosecond-suffix so
		// the regenerated name is deterministic-but-distinct.
		regen = fmt.Sprintf("%s_%d", regen, time.Now().UTC().UnixNano())
	}

	regenExists, err := cm.collectionExists(ctx, regen)
	if err != nil {
		return "", fmt.Errorf("check regenerated name %q: %w", regen, err)
	}
	if regenExists {
		return regen, ErrTimestampCollision
	}
	return regen, nil
}

// BlueGreenReport is the canonical JSON output shape for the
// QDRANT-007 TODO 11 admin reindex flow. JSON tags mirror the
// spec-literal field names exactly so the operator dashboard can
// parse the output without custom mapping.
//
// `old_collection` is the alias target BEFORE the reindex run
// starts (or "" when no alias was wired yet). `rollback_target`
// carries the same value on the success path AND when Ready=false
// — operators always get a rollback handle even when verification
// succeeds (see test 5 of the TODO 11 spec: "rollback_target
// valorizzato anche su success").
type BlueGreenReport struct {
	OldCollection  string `json:"old_collection"`
	NewCollection  string `json:"new_collection"`
	AliasSwapped   bool   `json:"alias_swapped"`
	RollbackTarget string `json:"rollback_target"`
	VerifierPassed bool   `json:"verifier_passed"`
}
