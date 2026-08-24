// Package outbox — state_writer.go (QDRANT-002 PR7).
//
// ClipsStateWriter is the load-bearing minimum surface the Dispatcher
// needs to flip the canonical media_assets.index_state column inside
// the same tx as the outbox_events INSERT (the producer step of
// EnqueueAndDelete).
//
// QDRANT-002 PR7 does NOT include a MultiClipsStateWriter because the
// PR2.6 media_assets consolidation folded every per-source
// *assets.ClipsRepository into the SAME SQLite table. The source
// dispatching that MultiClipsUpserter performs for UpsertClipTx is a
// backwards-compatibility shim for callers that used to write to
// separate per-source tables; index_state is a single-column flip
// against the unified table, so the underlying *assets.ClipsRepository
// satisfies the StateWriter interface directly. Production wires
// `repos.ClipsRepo` once; the multi-routing complexity stays where it
// still makes a difference (UpsertClipTx's per-source batching).
package outbox

import (
	"context"
	"database/sql"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ClipsStateWriter is the *assets.ClipsRepository method surface the
// Dispatcher needs for the SetIndexStateTx half of EnqueueAndDelete.
// Declared as an interface so unit tests can substitute a fake
// without pulling the full assets state-machine plus PR6's
// SetIndexState. Production concrete is *assets.ClipsRepository from
// internal/platform/sqlite/assets.
type ClipsStateWriter interface {
	SetIndexStateTx(ctx context.Context, tx *sql.Tx, id string, state asset.IndexState) error
}
