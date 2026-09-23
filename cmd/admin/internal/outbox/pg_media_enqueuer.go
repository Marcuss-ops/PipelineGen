package outbox

import (
	"context"
	"database/sql"
	"fmt"

	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

// PGMediaEnqueuer routes the admin embedding-backfill reindex requests
// onto the LIVE media index lane: the PostgreSQL outbox drained by
// pgmedia.PostgresIndexWorker (POSTGRES-MEDIA-CUTOVER).
//
// WHY this is not RepairAdapter: the SQLite outbox registers NO handler
// for asset.index.requested in ANY mode since the media index cutover
// (build_outbox_handlers.go fails composition if it ever does again), so
// every repair event written there ends in dead_letter with "no handler
// registered for event type asset.index.requested" and no embedding is
// ever produced. The media index plane has exactly ONE owner — the
// pgvector PostgresIndexWorker over the PostgreSQL outbox — and the
// admin repair path must speak to that owner (godlike/06: one owner per
// fact, including the enqueue side).
//
// The Enqueuer port's contentHash/force parameters are envelope
// concepts of the retired SQLite lane: pgmedia.ReindexRequester owns its
// canonical envelope, the source-version fingerprint and the
// terminal-conflict handling (an already-terminal request is re-keyed so
// the work actually re-runs instead of silently coalescing).
type PGMediaEnqueuer struct{ requester *pgmedia.ReindexRequester }

// NewPGMediaEnqueuer binds the canonical PostgreSQL media reindex
// requester (nil db yields an inert enqueuer that fails closed on use).
func NewPGMediaEnqueuer(db *sql.DB) *PGMediaEnqueuer {
	return &PGMediaEnqueuer{requester: pgmedia.NewReindexRequester(db)}
}

// EnqueueReindex implements indexing.Enqueuer on the PostgreSQL media
// outbox lane.
func (e *PGMediaEnqueuer) EnqueueReindex(ctx context.Context, assetID, _ string, _ bool) error {
	if e == nil || e.requester == nil {
		return fmt.Errorf("outbox.PGMediaEnqueuer: media PostgreSQL is not wired")
	}
	return e.requester.RequestIndex(ctx, assetID)
}

var _ interface {
	EnqueueReindex(ctx context.Context, assetID, contentHash string, force bool) error
} = (*PGMediaEnqueuer)(nil)
