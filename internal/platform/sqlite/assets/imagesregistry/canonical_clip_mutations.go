package imagesregistry

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// UpsertClipTx is the canonical transaction-bound clip mutation. The caller
// owns the transaction and remains responsible for emitting the matching
// outbox event before committing it.
func (c *SQLiteMediaCommitter) UpsertClipTx(ctx context.Context, tx *sql.Tx, clip *asset.Asset) error {
	if c == nil {
		return fmt.Errorf("canonical clip writer: media committer is unavailable")
	}
	return commitClipTxThroughCanonical(ctx, tx, clip, c)
}

// SetIndexStateTx is the canonical transaction-bound index-state mutation.
// It deliberately uses the exact *sql.Tx supplied by the dispatcher.

func (c *SQLiteMediaCommitter) SetIndexStateTx(ctx context.Context, tx *sql.Tx, assetID string, state asset.IndexState) error {
	if c == nil || c.assets == nil {
		return fmt.Errorf("canonical clip writer: asset committer is unavailable")
	}
	if tx == nil {
		return fmt.Errorf("canonical clip writer: transaction is required")
	}
	if assetID == "" {
		return fmt.Errorf("canonical clip writer: asset id is required")
	}
	if !state.Valid() {
		return fmt.Errorf("canonical clip writer: index state %q is invalid", state)
	}
	return UpdateMediaAssetIndexState(ctx, tx, assetID, string(state), "", "")
}

var _ persistence.CanonicalAssetWriter = (*SQLiteMediaCommitter)(nil)
