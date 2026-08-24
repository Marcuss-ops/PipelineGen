package collections

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/verification"
)

// ── Types ────────────────────────────────────────────────────────────

// EnsureResult is the outcome of EnsureSchema.
type EnsureResult struct {
	Collection   string `json:"collection"`
	AliasTarget  string `json:"alias_target"`
	Compatible   bool   `json:"compatible"`
	Created      bool   `json:"created"`
	AliasCreated bool   `json:"alias_created"`
}

// CollectionManager handles Qdrant collection lifecycle: inspection,
// candidate preparation, schema verification, alias promotion, and
// retention sweep.
//
// PR 6 (refactor/qdrant-index-document, §#5): split into the 8
// blue-green ops. PromoteCandidate is the ONLY function in the
// collection_manager_* files that calls cm.client.CreateAlias or
// cm.client.SwitchAlias. EnsureSchema is a thin orchestrator that
// calls the ops in sequence.
//
// QDRANT-ALIAS-CACHE (July 2026): OnAliasSwitch is an optional callback
// invoked after every successful PromoteCandidate. The Searcher wires
// its ResetSearchCache here so the alias-target cache is invalidated
// atomically with the alias switch.
type CollectionManager struct {
	client *transport.Client
	schema *schema.IndexSchema
	log    *zap.Logger

	verifyLedgerMu sync.RWMutex
	verifyLedger   map[string]bool

	// projectionMu protects the in-process lifecycle mirror. Durable
	// projection metadata remains owned by Media Registry; this mirror is
	// the fail-closed guard between build, validation and alias activation.
	projectionMu sync.RWMutex
	projections  map[string]mediaregistry.Projection
	// aliasMu serializes read/compare/switch/compensation sequences for
	// this manager. Qdrant's alias endpoint is atomic per request but does
	// not provide compare-and-swap semantics for the old target.
	aliasMu         sync.Mutex
	reindexVerifier *verification.ReindexVerifier
	registryLedger  mediaregistry.Ledger

	// OnAliasSwitch is called after a successful PromoteCandidate.
	// nil is safe — the callback is optional. The canonical consumer
	// is Searcher.ResetSearchCache, wired at runtime construction.
	OnAliasSwitch func()
}

// NewCollectionManager creates a CollectionManager bound to a schema.
func NewCollectionManager(client *transport.Client, schema *schema.IndexSchema, log *zap.Logger) *CollectionManager {
	if log == nil {
		log = zap.NewNop()
	}
	return &CollectionManager{
		client:       client,
		schema:       schema,
		log:          log,
		verifyLedger: make(map[string]bool),
		projections:  make(map[string]mediaregistry.Projection),
	}
}

// NewProjectionManager is the canonical semantic constructor for the
// Qdrant projection lifecycle. CollectionManager remains the concrete
// implementation used by existing composition roots.
func NewProjectionManager(client *transport.Client, schema *schema.IndexSchema, log *zap.Logger) *CollectionManager {
	return NewCollectionManager(client, schema, log)
}

// SetReindexVerifier wires the full failure-oriented validation gate. It is
// optional for schema-only/admin callers; production runtime wiring supplies
// it so activation requires complete point, ID, payload, sequence-independent
// and scan validation rather than only a non-empty collection.
func (cm *CollectionManager) SetReindexVerifier(verifier *verification.ReindexVerifier) {
	cm.projectionMu.Lock()
	cm.reindexVerifier = verifier
	cm.projectionMu.Unlock()
}

// SetRegistryLedger wires the canonical Media Registry as the source of the
// current registry sequence and durable projection lifecycle metadata.
func (cm *CollectionManager) SetRegistryLedger(ctx context.Context, ledger mediaregistry.Ledger) error {
	cm.projectionMu.Lock()
	cm.registryLedger = ledger
	cm.projectionMu.Unlock()
	if ledger == nil {
		return nil
	}
	reader, ok := ledger.(mediaregistry.ProjectionReader)
	if !ok {
		return nil
	}
	projections, err := reader.ListProjections(ctx)
	if err != nil {
		// Keep the ledger wired during the migration window when a
		// deliberately minimal admin/test database predates projection
		// registry migration 203. Projection operations will still fail
		// closed when they attempt to read the canonical sequence.
		if strings.Contains(err.Error(), "no such table: projection_registry") {
			return nil
		}
		cm.projectionMu.Lock()
		cm.registryLedger = nil
		cm.projectionMu.Unlock()
		return fmt.Errorf("hydrate projection registry: %w", err)
	}
	projections, err = cm.reconcileDuplicateActiveProjections(ctx, projections, ledger)
	if err != nil {
		return fmt.Errorf("reconcile duplicate active projections: %w", err)
	}
	sequence, err := ledger.LatestEventSequence(ctx)
	if err != nil {
		if strings.Contains(err.Error(), "no such table: registry_events") {
			return nil
		}
		cm.projectionMu.Lock()
		cm.registryLedger = nil
		cm.projectionMu.Unlock()
		return fmt.Errorf("hydrate projection registry sequence: %w", err)
	}
	if err := validateHydratedProjections(projections, sequence); err != nil {
		cm.projectionMu.Lock()
		cm.registryLedger = nil
		cm.projectionMu.Unlock()
		return err
	}
	cm.projectionMu.Lock()
	for _, projection := range projections {
		cm.projections[projection.ProjectionID] = projection
	}
	cm.projectionMu.Unlock()
	return nil
}

// reconcileDuplicateActiveProjections repairs the crash/migration residue
// where Qdrant's alias has already moved but SQLite still contains the old
// projection as ACTIVE. The alias is the external atomic fact; only the
// projection matching its target may remain ACTIVE. If the alias cannot be
// resolved, or does not match exactly one active projection, startup remains
// fail-closed.
func (cm *CollectionManager) reconcileDuplicateActiveProjections(ctx context.Context, projections []mediaregistry.Projection, ledger mediaregistry.Ledger) ([]mediaregistry.Projection, error) {
	activeByAlias := make(map[string][]int)
	for i, projection := range projections {
		if projection.Status == string(mediaregistry.ProjectionActive) {
			activeByAlias[projection.AliasName] = append(activeByAlias[projection.AliasName], i)
		}
	}
	for alias, indexes := range activeByAlias {
		if len(indexes) < 2 {
			continue
		}
		if cm.client == nil {
			return nil, fmt.Errorf("alias %q has %d ACTIVE projections but Qdrant client is unavailable", alias, len(indexes))
		}
		target, err := cm.client.GetAliasTarget(ctx, alias)
		if err != nil {
			return nil, fmt.Errorf("resolve alias %q while repairing ACTIVE projections: %w", alias, err)
		}
		kept := 0
		for _, index := range indexes {
			if projections[index].CollectionName == target {
				kept++
			}
		}
		if kept != 1 {
			return nil, fmt.Errorf("alias %q target %q matches %d ACTIVE projections", alias, target, kept)
		}
		for _, index := range indexes {
			if projections[index].CollectionName == target {
				continue
			}
			projections[index].Status = string(mediaregistry.ProjectionRetired)
			if err := ledger.RegisterProjection(ctx, projections[index]); err != nil {
				return nil, fmt.Errorf("retire stale projection %q: %w", projections[index].ProjectionID, err)
			}
			cm.log.Warn("repaired stale ACTIVE projection during hydration",
				zap.String("projection_id", projections[index].ProjectionID),
				zap.String("alias", alias),
				zap.String("active_collection", target))
		}
	}
	return projections, nil
}

// ErrPromoteWithoutVerify is the typed error returned by
// PromoteCandidate when verifyLedger[candidate] is missing.
var ErrPromoteWithoutVerify = errors.New("qdrant: PromoteCandidate requires a prior VerifyCandidate success (PR 6 §#5/§#6.3)")

// EnsureSchema guarantees the runtime alias points to a ready
// candidate. PR 6 §#5: EnsureSchema is a thin orchestrator that
// delegates to the ops in sequence:
//
//	InspectRuntime     → short-circuit on already-compatible target
//	PrepareCandidate   → create physical + payload indexes (NO alias)
//	ReindexCandidate   → no-op stub (admin reindex is explicit)
//	VerifyCandidate    → schema match + mark verifyLedger
//	PromoteCandidate   → writes the alias (verifyLedger required)
func (cm *CollectionManager) EnsureSchema(ctx context.Context) (*EnsureResult, error) {
	if err := cm.schema.Validate(); err != nil {
		return nil, fmt.Errorf("invalid schema: %w", err)
	}
	candidate := cm.schema.CanonicalName()

	// On restart, repair the durable lifecycle mirror before deciding
	// whether the runtime alias is compatible. This handles the crash
	// window after Qdrant's atomic alias switch and before SQLite recorded
	// ACTIVE, without issuing a second alias mutation.
	if err := cm.ReconcileProjection(ctx); err != nil {
		return nil, err
	}
	target, diff, err := cm.InspectRuntime(ctx)
	if err != nil {
		return nil, fmt.Errorf("inspect runtime: %w", err)
	}
	if target != "" && diff != nil && diff.Compatible {
		cm.log.Info("alias points to compatible collection",
			zap.String("alias", cm.schema.RuntimeAlias),
			zap.String("target", target))
		cm.MarkVerified(target)
		return &EnsureResult{
			Collection:  target,
			AliasTarget: target,
			Compatible:  true,
			Created:     false,
		}, nil
	}

	// Route the complete startup lifecycle through the Projection Manager:
	// BUILDING -> VALIDATING -> READY -> ACTIVE. Legacy callers may still
	// use the lower-level blue/green methods during migration, but the
	// canonical startup path must persist lifecycle state before alias use.
	if err := cm.BuildProjection(ctx, candidate, candidate, 0); err != nil {
		return nil, fmt.Errorf("build projection: %w", err)
	}
	if _, err := cm.ValidateProjection(ctx, candidate, 0, 0); err != nil {
		return nil, fmt.Errorf("validate projection: %w", err)
	}
	if err := cm.ActivateProjection(ctx, candidate, 0); err != nil {
		return nil, fmt.Errorf("activate projection: %w", err)
	}

	cm.log.Info("ensure-schema: created + promoted",
		zap.String("collection", candidate),
		zap.String("alias", cm.schema.RuntimeAlias),
		zap.String("version", cm.schema.Version))

	return &EnsureResult{
		Collection:   candidate,
		AliasTarget:  candidate,
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
func (cm *CollectionManager) InspectCollection(ctx context.Context, name string) (*schema.CollectionInfo, error) {
	return cm.client.GetCollection(ctx, name)
}

// CompareActiveCollection compares the active (aliased) collection against the
// expected schema and returns the diff.
func (cm *CollectionManager) CompareActiveCollection(ctx context.Context) (*schema.SchemaDiff, error) {
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
	return schema.CompareSchema(cm.schema, info), nil
}

// ── Snapshot / Restore (QDRANT-005, June 2026) ───────────────────────

func (cm *CollectionManager) CreateSnapshot(ctx context.Context, collection string) (*schema.SnapshotDescription, error) {
	cm.log.Info("creating snapshot", zap.String("collection", collection))
	snap, err := cm.client.CreateSnapshot(ctx, collection)
	if err != nil {
		return nil, fmt.Errorf("create snapshot %q: %w", collection, err)
	}
	cm.log.Info("snapshot created", zap.String("collection", collection), zap.String("name", snap.Name), zap.Int64("size", snap.Size))
	return snap, nil
}

func (cm *CollectionManager) ListSnapshots(ctx context.Context) ([]schema.SnapshotDescription, error) {
	target, err := cm.client.GetAliasTarget(ctx, cm.schema.RuntimeAlias)
	if err != nil {
		return nil, fmt.Errorf("get active collection: %w", err)
	}
	if target == "" {
		return nil, fmt.Errorf("no active collection for alias %q", cm.schema.RuntimeAlias)
	}
	return cm.client.ListSnapshots(ctx, target)
}

func (cm *CollectionManager) RestoreSnapshot(ctx context.Context, collection, snapshotURL string) error {
	cm.log.Warn("restoring collection from snapshot", zap.String("collection", collection), zap.String("snapshot_url", snapshotURL))
	return cm.client.RestoreSnapshot(ctx, collection, snapshotURL)
}

// ── Bootstrap / Create / Deprecated ───────────────────────────────────

// BootstrapEmptyCollection creates an empty physical collection +
// payload indexes WITHOUT aliasing.
func (cm *CollectionManager) BootstrapEmptyCollection(ctx context.Context, candidate string, allowEmpty bool) error {
	if !allowEmpty {
		return fmt.Errorf("BootstrapEmptyCollection called without --allow-empty; use PrepareCandidate for the canonical data-ready path")
	}
	cm.log.Info("bootstrapping empty collection (no alias yet)",
		zap.String("candidate", candidate))
	if err := cm.createPhysicalCollection(ctx, candidate); err != nil {
		return fmt.Errorf("create physical %q: %w", candidate, err)
	}
	if err := cm.createPayloadIndexes(ctx, candidate); err != nil {
		return fmt.Errorf("create payload indexes for %q: %w", candidate, err)
	}
	return nil
}

// CreateCollection creates a new physical collection with the schema's
// vector config and payload indexes. Used by the admin reindex command.
func (cm *CollectionManager) CreateCollection(ctx context.Context, name string) error {
	if err := cm.createPhysicalCollection(ctx, name); err != nil {
		return fmt.Errorf("create physical collection %q: %w", name, err)
	}
	if err := cm.createPayloadIndexes(ctx, name); err != nil {
		return fmt.Errorf("create payload indexes for %q: %w", name, err)
	}
	return nil
}
