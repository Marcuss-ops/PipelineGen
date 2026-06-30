package qdrant

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// ── Promote ────────────────────────────────────────────────────────────

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
