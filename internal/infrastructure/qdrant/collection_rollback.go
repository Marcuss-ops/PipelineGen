package qdrant

import (
	"context"

	"go.uber.org/zap"
)

// ── Rollback ───────────────────────────────────────────────────────────

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
