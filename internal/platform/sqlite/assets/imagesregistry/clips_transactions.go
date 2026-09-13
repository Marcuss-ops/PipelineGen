package imagesregistry

import (
	"context"
	"database/sql"
	"fmt"
)

// BeginTx opens a caller-owned transaction for operational (non-media)
// dispatcher work.
//
// MEDIA-SSOT (September 2026): this file no longer carries a tx-bound media
// mutation. The former `UpsertClipTx` / `SetIndexStateTx` compatibility
// surfaces delegated into `persistence.CanonicalAssetWriter`, which accepted
// a caller-owned `*sql.Tx`. That type was engine-agnostic while the media
// domain is owned by PostgreSQL, so the seam let the SQLite outbox dispatcher
// hand its own transaction to the PostgreSQL media writer — a defect that
// only surfaced at first runtime statement. The canonical writer boundary now
// exposes self-owned entry points only (CommitAndIndex / CommitAsset /
// CommitDiscoveredAssetAndIndex), so there is nothing left to delegate to and
// the construct is no longer representable.
func (r *ClipsRepository) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("clips.BeginTx: database is required")
	}
	return r.db.BeginTx(ctx, opts)
}
