package qdrant

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"go.uber.org/zap"
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
type CollectionManager struct {
	client *Client
	schema *IndexSchema
	log    *zap.Logger

	verifyLedgerMu sync.RWMutex
	verifyLedger   map[string]bool
}

// NewCollectionManager creates a CollectionManager bound to a schema.
func NewCollectionManager(client *Client, schema *IndexSchema, log *zap.Logger) *CollectionManager {
	return &CollectionManager{
		client:       client,
		schema:       schema,
		log:          log,
		verifyLedger: make(map[string]bool),
	}
}

// ErrPromoteWithoutVerify is the typed error returned by
// PromoteCandidate when verifyLedger[candidate] is missing.
var ErrPromoteWithoutVerify = errors.New("qdrant: PromoteCandidate requires a prior VerifyCandidate success (PR 6 §#5/§#6.3)")

// ── verifyLedger helpers ─────────────────────────────────────────────

func (cm *CollectionManager) markVerified(name string) {
	cm.verifyLedgerMu.Lock()
	cm.verifyLedger[name] = true
	cm.verifyLedgerMu.Unlock()
}

func (cm *CollectionManager) consumeVerified(name string) bool {
	cm.verifyLedgerMu.Lock()
	defer cm.verifyLedgerMu.Unlock()
	ok := cm.verifyLedger[name]
	delete(cm.verifyLedger, name)
	return ok
}

func (cm *CollectionManager) resetVerified(name string) {
	cm.verifyLedgerMu.Lock()
	delete(cm.verifyLedger, name)
	cm.verifyLedgerMu.Unlock()
}

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
	candidate := cm.schema.physicalName()

	target, diff, err := cm.InspectRuntime(ctx)
	if err != nil {
		return nil, fmt.Errorf("inspect runtime: %w", err)
	}
	if target != "" && diff != nil && diff.Compatible {
		cm.log.Info("alias points to compatible collection",
			zap.String("alias", cm.schema.RuntimeAlias),
			zap.String("target", target))
		cm.markVerified(target)
		return &EnsureResult{
			Collection:  target,
			AliasTarget: target,
			Compatible:  true,
			Created:     false,
		}, nil
	}

	if err := cm.PrepareCandidate(ctx, candidate); err != nil {
		return nil, fmt.Errorf("prepare candidate: %w", err)
	}
	if err := cm.ReindexCandidate(ctx, candidate); err != nil {
		return nil, fmt.Errorf("reindex candidate: %w", err)
	}
	if err := cm.VerifyCandidate(ctx, candidate); err != nil {
		return nil, fmt.Errorf("verify candidate: %w", err)
	}
	if err := cm.PromoteCandidate(ctx, candidate); err != nil {
		return nil, fmt.Errorf("promote candidate: %w", err)
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
func (cm *CollectionManager) InspectCollection(ctx context.Context, name string) (*CollectionInfo, error) {
	return cm.client.GetCollection(ctx, name)
}

// CompareActiveCollection compares the active (aliased) collection against the
// expected schema and returns the diff.
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

// ── Snapshot / Restore (QDRANT-005, June 2026) ───────────────────────

func (cm *CollectionManager) CreateSnapshot(ctx context.Context, collection string) (*SnapshotDescription, error) {
	cm.log.Info("creating snapshot", zap.String("collection", collection))
	snap, err := cm.client.CreateSnapshot(ctx, collection)
	if err != nil {
		return nil, fmt.Errorf("create snapshot %q: %w", collection, err)
	}
	cm.log.Info("snapshot created", zap.String("collection", collection), zap.String("name", snap.Name), zap.Int64("size", snap.Size))
	return snap, nil
}

func (cm *CollectionManager) ListSnapshots(ctx context.Context) ([]SnapshotDescription, error) {
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

// SwitchAlias atomically switches the runtime alias from oldTarget to newTarget.
// DEPRECATED: use PromoteCandidate or RollbackCandidate directly.
func (cm *CollectionManager) SwitchAlias(ctx context.Context, oldTarget, newTarget string) error {
	return cm.RollbackCandidate(ctx, oldTarget, newTarget)
}

// RollbackAlias switches the alias back to oldTarget.
// DEPRECATED: use RollbackCandidate directly.
func (cm *CollectionManager) RollbackAlias(ctx context.Context, currentTarget, rollbackTarget string) error {
	return cm.RollbackCandidate(ctx, currentTarget, rollbackTarget)
}

// ── Rollback ───────────────────────────────────────────────────────────
// Relocated from collection_rollback.go (Phase 5 consolidation, June 2026).

// RollbackCandidate switches the alias back to `rollbackTarget`.
// Used by `cmd/admin/reconcile_qdrant.go` to undo a failed reindex
// promote. PR 6 §#5: RollbackCandidate is also a (re)writer; it is
// the ONLY safe counterpart to PromoteCandidate.
func (cm *CollectionManager) RollbackCandidate(ctx context.Context, currentTarget, rollbackTarget string) error {
	cm.log.Warn("rolling back alias",
		zap.String("alias", cm.schema.RuntimeAlias),
		zap.String("from", currentTarget),
		zap.String("to", rollbackTarget))
	return cm.client.SwitchAlias(ctx, cm.schema.RuntimeAlias, currentTarget, rollbackTarget)
}

// ── Verify ─────────────────────────────────────────────────────────────
// Relocated from collection_verify.go (Phase 5 consolidation, June 2026).

// VerifyCandidate ensures the candidate's schema matches cm.schema
// via CompareSchema. On success, sets verifyLedger[candidate] = true
// so PromoteCandidate can run. Returns ErrSchemaIncompatible wrapped
// when the candidate's schema doesn't match.
func (cm *CollectionManager) VerifyCandidate(ctx context.Context, candidate string) error {
	info, err := cm.client.GetCollection(ctx, candidate)
	if err != nil {
		return fmt.Errorf("get collection %q: %w", candidate, err)
	}
	diff := CompareSchema(cm.schema, info)
	if !diff.Compatible {
		return fmt.Errorf("%w for %q: missing_vectors=%v dimension_mismatches=%d distance_mismatches=%v",
			NewErrSchemaIncompatible(diff), candidate,
			diff.MissingVectors, len(diff.DimensionMismatches), diff.DistanceMismatches)
	}
	cm.markVerified(candidate)
	return nil
}

// ── Promote + Reindex ──────────────────────────────────────────────────
// Relocated from collection_promote.go (Phase 5 consolidation, June 2026).

// PromoteCandidate writes the runtime alias to point at `candidate`.
// This is the ONLY method in CollectionManager that writes the
// alias (no other function in the collection_manager_* files calls
// cm.client.CreateAlias or cm.client.SwitchAlias outside
// RollbackCandidate's switch-back). Fails with ErrPromoteWithoutVerify
// if verifyLedger[candidate] is missing (PR 6 §#6.3 invariant).
func (cm *CollectionManager) PromoteCandidate(ctx context.Context, candidate string) error {
	if !cm.consumeVerified(candidate) {
		return fmt.Errorf("%w: candidate %q (call VerifyCandidate first)", ErrPromoteWithoutVerify, candidate)
	}
	cm.log.Info("promoting candidate to runtime alias",
		zap.String("alias", cm.schema.RuntimeAlias),
		zap.String("target", candidate))
	return cm.client.CreateAlias(ctx, cm.schema.RuntimeAlias, candidate)
}

// ReindexCandidate is the orchestrator marker step in EnsureSchema's
// chain. In this PR it's a no-op stub because the actual reindex
// is owned by the admin `cmd/admin/reindex_qdrant.go` command.
// EnsureSchema keeps the call so the contract is explicit.
func (cm *CollectionManager) ReindexCandidate(ctx context.Context, candidate string) error {
	cm.log.Debug("ReindexCandidate is a no-op stub in EnsureSchema orchestrator; explicit reindex lives in cmd/admin/reindex_qdrant.go",
		zap.String("candidate", candidate))
	return nil
}
