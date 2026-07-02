package qdrant

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// ── Promote + Reindex ──────────────────────────────────────────────────

// PromoteCandidate writes the runtime alias to point at `candidate`.
// This is the ONLY method in CollectionManager that writes the
// alias (no other function in the collection_manager_* files calls
// cm.client.CreateAlias or cm.client.SwitchAlias outside
// RollbackCandidate's switch-back). Fails with ErrPromoteWithoutVerify
// if verifyLedger[candidate] is missing (PR 6 §#6.3 invariant).
//
// QDRANT-ALIAS-CACHE (July 2026): after a successful alias write,
// PromoteCandidate invokes cm.OnAliasSwitch (if non-nil) so downstream
// consumers (e.g. Searcher.ResetSearchCache) can invalidate their
// cached alias target atomically with the switch.
func (cm *CollectionManager) PromoteCandidate(ctx context.Context, candidate string) error {
	if !cm.consumeVerified(candidate) {
		return fmt.Errorf("%w: candidate %q (call VerifyCandidate first)", ErrPromoteWithoutVerify, candidate)
	}
	cm.log.Info("promoting candidate to runtime alias",
		zap.String("alias", cm.schema.RuntimeAlias),
		zap.String("target", candidate))
	if err := cm.client.CreateAlias(ctx, cm.schema.RuntimeAlias, candidate); err != nil {
		return err
	}
	// Invalidate downstream caches atomically with the alias switch.
	if cm.OnAliasSwitch != nil {
		cm.OnAliasSwitch()
	}
	return nil
}

// ReindexCandidate checks whether the candidate collection has points.
// When PointTotal is zero the collection was created by PrepareCandidate
// but never backfilled — returns ErrReindexRequired to block promotion.
// The operator must run `go run ./cmd/admin reindex-qdrant --apply` to
// backfill data before the server can start with a ready Qdrant index.
func (cm *CollectionManager) ReindexCandidate(ctx context.Context, candidate string) error {
	info, err := cm.client.GetCollection(ctx, candidate)
	if err != nil {
		return fmt.Errorf("reindex candidate: get collection %q: %w", candidate, err)
	}
	if info.PointTotal == 0 {
		cm.log.Warn("collection has zero points — reindex required before promotion",
			zap.String("candidate", candidate))
		return &ErrReindexRequired{Collection: candidate}
	}
	cm.log.Debug("reindex candidate: collection has points, proceeding",
		zap.String("candidate", candidate),
		zap.Int("points", info.PointTotal))
	return nil
}
