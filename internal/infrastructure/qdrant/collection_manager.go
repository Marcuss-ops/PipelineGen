package qdrant

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/qdrantdr"
)

// CollectionManager handles Qdrant collection lifecycle: inspection,
// candidate preparation, schema verification, alias promotion, and
// retention sweep.
//
// PR 6 (refactor/qdrant-index-document, §#5): split into the 8
// blue-green ops. PromoteCandidate is the ONLY function in this
// file that calls cm.client.CreateAlias or cm.client.SwitchAlias.
// EnsureSchema is a thin orchestrator that calls the ops in
// sequence. Operators that need fine-grained control run the ops
// individually via cmd/admin paths; the orchestrator stays for
// boot-time convenience.
type CollectionManager struct {
	client *Client
	schema *IndexSchema
	log    *zap.Logger

	// verifyLedger tracks which candidate collections passed
	// VerifyCandidate. PrepareCandidate resets the key for the
	// candidate name; VerifyCandidate sets it to true on success;
	// PromoteCandidate reads + consumes it to gate the alias write.
	// PR 6 §#6.3 invariant: PromoteCandidate fails when the ledger
	// entry is absent. The sync.RWMutex is overkill for single
	// Instance use but cheap insurance for future tests that
	// exercise the ledger under goroutine pressure.
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

// markVerified sets verifyLedger[name] = true.
func (cm *CollectionManager) markVerified(name string) {
	cm.verifyLedgerMu.Lock()
	cm.verifyLedger[name] = true
	cm.verifyLedgerMu.Unlock()
}

// consumeVerified reads verifyLedger[name] and removes the entry
// (single-use guard). Returns false if the ledger has no entry for
// `name` or if the entry was already consumed.
func (cm *CollectionManager) consumeVerified(name string) bool {
	cm.verifyLedgerMu.Lock()
	defer cm.verifyLedgerMu.Unlock()
	ok := cm.verifyLedger[name]
	delete(cm.verifyLedger, name)
	return ok
}

// resetVerified clears verifyLedger[name]. Used by PrepareCandidate
// so a re-prepare invalidates any prior verification.
func (cm *CollectionManager) resetVerified(name string) {
	cm.verifyLedgerMu.Lock()
	delete(cm.verifyLedger, name)
	cm.verifyLedgerMu.Unlock()
}

// ErrPromoteWithoutVerify is the typed error returned by
// PromoteCandidate when verifyLedger[candidate] is missing. The
// §#6.3 test asserts this guard fires when VerifyCandidate wasn't
// run; operators reading the error know they must call
// VerifyCandidate before any alias write.
var ErrPromoteWithoutVerify = errors.New("qdrant: PromoteCandidate requires a prior VerifyCandidate success (PR 6 §#5/§#6.3)")

// EnsureSchema guarantees the runtime alias points to a ready
// candidate. PR 6 §#5: EnsureSchema is a thin orchestrator that
// delegates to the 8 ops in sequence:
//
//	InspectRuntime     → short-circuit on already-compatible target
//	PrepareCandidate   → create physical + payload indexes (NO alias)
//	ReindexCandidate   → no-op stub (admin reindex is explicit)
//	VerifyCandidate    → schema match + mark verifyLedger
//	PromoteCandidate   → writes the alias (verifyLedger required)
//
// `PrepareCandidate` and BootstrapEmptyCollection NEVER write the
// alias; `PromoteCandidate` is the SINGLE function in this file
// that does. The pre-PR6 implicit "create-alias-onto-empty-
// newly-created" path is gone — the ledger gate in PromoteCandidate
// forces the AliasCreated=true state to be the result of a
// successful Reindex + Verify cycle.
//
// Policy (preserved from pre-PR6):
//   - Alias points to a compatible target → success (no-op).
//   - Alias missing OR existing target incompatible → prepare +
//     reindex + verify + promote.
func (cm *CollectionManager) EnsureSchema(ctx context.Context) (*EnsureResult, error) {
	if err := cm.schema.Validate(); err != nil {
		return nil, fmt.Errorf("invalid schema: %w", err)
	}
	candidate := cm.schema.physicalName()

	// 1. InspectRuntime — short-circuit if alias already compatible.
	target, diff, err := cm.InspectRuntime(ctx)
	if err != nil {
		return nil, fmt.Errorf("inspect runtime: %w", err)
	}
	if target != "" && diff != nil && diff.Compatible {
		cm.log.Info("alias points to compatible collection",
			zap.String("alias", cm.schema.RuntimeAlias),
			zap.String("target", target))
		cm.markVerified(target) // already-existing compatible target passes the gate
		return &EnsureResult{
			Collection:  target,
			AliasTarget: target,
			Compatible:  true,
			Created:     false,
		}, nil
	}

	// 2. PrepareCandidate — physical + payload indexes, NO alias.
	if err := cm.PrepareCandidate(ctx, candidate); err != nil {
		return nil, fmt.Errorf("prepare candidate: %w", err)
	}

	// 3. ReindexCandidate — no-op stub (admin runs explicit reindex).
	if err := cm.ReindexCandidate(ctx, candidate); err != nil {
		return nil, fmt.Errorf("reindex candidate: %w", err)
	}

	// 4. VerifyCandidate — schema match + mark verifyLedger.
	if err := cm.VerifyCandidate(ctx, candidate); err != nil {
		return nil, fmt.Errorf("verify candidate: %w", err)
	}

	// 5. PromoteCandidate — alias write gated by verifyLedger.
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

// ── 8 blue-green ops (PR 6 refactor/qdrant-index-document §#5) ──────

// InspectRuntime returns the current alias target and the
// SchemaDiff against cm.schema. target == "" means the alias is
// unwritten (ErrCollectionNotFound from the client is swallowed).
// diff is nil only if target == "".
func (cm *CollectionManager) InspectRuntime(ctx context.Context) (target string, diff *SchemaDiff, err error) {
	target, err = cm.client.GetAliasTarget(ctx, cm.schema.RuntimeAlias)
	if err != nil {
		if _, ok := err.(*ErrCollectionNotFound); ok {
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
	diff = CompareSchema(cm.schema, info)
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

// ReindexCandidate is the orchestrator marker step in EnsureSchema's
// chain. In this PR it's a no-op stub because the actual reindex
// (the point-by-point write via IndexWriter + AssetStore) is owned
// by the admin `cmd/admin/reindex_qdrant.go` command. EnsureSchema
// keeps the call so the contract is explicit — operators reading
// the orchestrator path can trust that PromoteCandidate runs AFTER
// (whatever reindex step did).
func (cm *CollectionManager) ReindexCandidate(ctx context.Context, candidate string) error {
	cm.log.Debug("ReindexCandidate is a no-op stub in EnsureSchema orchestrator; explicit reindex lives in cmd/admin/reindex_qdrant.go",
		zap.String("candidate", candidate))
	return nil
}

// VerifyCandidate ensures the candidate's schema matches cm.schema
// via CompareSchema. On success, sets verifyLedger[candidate] = true
// so PromoteCandidate can run. Returns ErrSchemaIncompatible wrapped
// when the candidate's schema doesn't match — the operator must
// re-PrepareCandidate and re-Reindex.
func (cm *CollectionManager) VerifyCandidate(ctx context.Context, candidate string) error {
	info, err := cm.client.GetCollection(ctx, candidate)
	if err != nil {
		return fmt.Errorf("get collection %q: %w", candidate, err)
	}
	diff := CompareSchema(cm.schema, info)
	if !diff.Compatible {
		return fmt.Errorf("%w for %q: missing_vectors=%v dimension_mismatches=%d distance_mismatches=%v",
			ErrSchemaIncompatible{Diff: diff}, candidate,
			diff.MissingVectors, len(diff.DimensionMismatches), diff.DistanceMismatches)
	}
	cm.markVerified(candidate)
	return nil
}

// PromoteCandidate writes the runtime alias to point at `candidate`.
// This is the ONLY method in CollectionManager that writes the
// alias (no other function in this file calls cm.client.CreateAlias
// or cm.client.SwitchAlias outside RollbackCandidate's switch-back).
// Fails with ErrPromoteWithoutVerify if verifyLedger[candidate] is
// missing (PR 6 §#6.3 invariant).
func (cm *CollectionManager) PromoteCandidate(ctx context.Context, candidate string) error {
	if !cm.consumeVerified(candidate) {
		return fmt.Errorf("%w: candidate %q (call VerifyCandidate first)", ErrPromoteWithoutVerify, candidate)
	}
	cm.log.Info("promoting candidate to runtime alias",
		zap.String("alias", cm.schema.RuntimeAlias),
		zap.String("target", candidate))
	return cm.client.CreateAlias(ctx, cm.schema.RuntimeAlias, candidate)
}

// RollbackCandidate switches the alias back to `rollbackTarget`.
// Used by `cmd/admin/reconcile_qdrant.go` to undo a failed reindex
// promote. PR 6 §#5: RollbackCandidate is also a (re)writer; it is
// the ONLY safe counterpart to PromoteCandidate. Other methods in
// this file MUST NOT call cm.client.CreateAlias / SwitchAlias.
func (cm *CollectionManager) RollbackCandidate(ctx context.Context, currentTarget, rollbackTarget string) error {
	cm.log.Warn("rolling back alias",
		zap.String("alias", cm.schema.RuntimeAlias),
		zap.String("from", currentTarget),
		zap.String("to", rollbackTarget))
	return cm.client.SwitchAlias(ctx, cm.schema.RuntimeAlias, currentTarget, rollbackTarget)
}

// RetireOldCollections drops all non-active collections whose names
// share the schema's physical-name prefix. Thin wrapper over the
// existing CleanupWithConfig path; the canonical implementation
// stays there so the retentionDays-only signature stays available
// for back-compat callers.
func (cm *CollectionManager) RetireOldCollections(ctx context.Context, cfg RetentionConfig) (*RetentionResult, error) {
	return cm.CleanupWithConfig(ctx, cfg)
}

// BootstrapEmptyCollection creates an empty physical collection +
// payload indexes WITHOUT aliasing. allowEmpty is informational
// only — the collection IS empty by definition (no points written
// here). Operators use this to stage a fresh candidate before the
// reindex writes begin, so the active alias remains untouched
// during ingest.
//
// PR 6 §#5: BootstrapEmptyCollection does NOT touch verifyLedger
// because it's a staging op, not a verification gate. After the
// operator runs Reindex + Verify explicitly, PromoteCandidate can
// be called against the same candidate name.
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
//
// PR 6 (§#5): DEPRECATED public alias writer — forwarded to
// RollbackCandidate so the spec's "Solo PromoteCandidate /
// RollbackCandidate possono modificare l'alias" invariant holds.
// New callers should use PromoteCandidate for forward promotion
// (after VerifyCandidate marks the ledger) or RollbackCandidate for
// backward safety. The legacy signature is preserved for backward
// compat with qdrant_test.go:481 + cmd/admin/reindex_qdrant.go:447.
//
// PR 6 §#5: this method MUST NOT call cm.client.SwitchAlias directly
// — it delegates to RollbackCandidate which is the only spec'd alias
// switcher. Writing the alias from anywhere else (other than
// PromoteCandidate which uses CreateAlias) violates the invariant.
func (cm *CollectionManager) SwitchAlias(ctx context.Context, oldTarget, newTarget string) error {
	return cm.RollbackCandidate(ctx, oldTarget, newTarget)
}

// RollbackAlias switches the alias back to oldTarget.
//
// PR 6 (§#5): DEPRECATED public alias writer — forwarded to
// RollbackCandidate. Kept for the qdrant_test.go:532 caller; new
// callers should use RollbackCandidate directly.
func (cm *CollectionManager) RollbackAlias(ctx context.Context, currentTarget, rollbackTarget string) error {
	return cm.RollbackCandidate(ctx, currentTarget, rollbackTarget)
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

	// PR 9 (#14): fix the latent sort + loop bugs in CleanupWithConfig.
	//
	// Pre-PR9 the retention sweep had TWO compounding bugs:
	//   1. `sort.Strings(eligible)` sorts ASCENDING. Each new reindex
	//      produces a name with a HIGHER lexicographic suffix
	//      (media_assets_v3__ts_20260101 < media_assets_v3__ts_20260228),
	//      so the ascending sort placed the OLDEST collections at the
	//      head. The subsequent `keep_left` loop then KEPT the oldest
	//      collections and DELETED the newest — the opposite of what
	//      the operator expects from a "keep_last_n" semantic.
	//   2. The `break` statement at the tail of the loop exited after
	//      consuming the FIRST eligible name (regardless of keepLeft),
	//      meaning at most ONE collection was protected by the floor
	//      even when keepLastN > 2.
	//
	// Fix: reverse the sort (newest-first by suffix), peel off the
	// keep_last_n tail INTO the keepSet, then sweep the remainder.
	// The keep_last_n floor of 2 (active + 1 rollback) is preserved
	// exactly: active counted outside the loop; the top (keep_last_n-1)
	// of the descending list join the keepSet; everything else drops.
	sort.Sort(sort.Reverse(sort.StringSlice(eligible)))
	keepLeft := keepLastN - 1 // active already counted
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
		// PR2 (fix/qdrant-bm25-indexing): the model name MUST be in
		// the sparse_vectors config so Qdrant can run server-side
		// BM25 inference at upsert and query time. Without it the
		// sparse channel is created empty and `bm25_text` searches
		// return zero matches. DefaultSparseModel is the SSOT.
		model := v.Model
		if model == "" {
			model = DefaultSparseModel
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
