// Package reconcile — reconcile_qdrant.go (RETIRED, media demolition
// September 2026).
//
// The QDRANT-005B media_assets reconciler compared the SQLite media
// catalog against the Qdrant media_assets projection and dispatched
// outbox repairs. With the PostgreSQL + pgvector media SSOT the
// SQLite → Qdrant media projection chain no longer exists: SQLite media
// writes are demolished and the media vector plane lives inside the
// PostgreSQL SSOT (media_embeddings), where parity is enforced by the
// transactional commit path itself — there is nothing left to reconcile.
//
// `reconcile-qdrant` AND `reindex-qdrant` are BOTH retired media-plane
// commands. The Qdrant media collection (`media_assets`, runtime alias
// `media_assets_current`) is no longer a projection of anything, so neither
// a parity repair nor an in-place rebuild can be meaningful: the media index
// plane is pgvector and is rebuilt by `pgmedia.PostgresIndexWorker`.
//
// Both subcommands stay REGISTERED and fail LOUDLY with a typed retirement
// error, so existing operator scripts surface the retirement instead of a
// silent command-not-found. Rebuilding a genuinely non-media Qdrant
// projection (mediamemory frame concepts) is owned by that capability's own
// pipeline, not by these media commands.
package reconcile

import (
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"
	"go.uber.org/zap"
)

// ErrMediaProjectionRetired is returned by the retired media reconciler.
var ErrMediaProjectionRetired = errors.New("reconcile-qdrant: the SQLite→Qdrant media_assets projection was retired with the PostgreSQL media-SSOT cutover (media demolition, September 2026); media parity is enforced transactionally inside PostgreSQL — use 'backfill-media-postgres --verify-only' for media parity evidence")

// ErrMediaReindexRetired is returned by the retired media reindex command.
var ErrMediaReindexRetired = errors.New("reindex-qdrant: rebuilding the Qdrant media_assets collection was retired with the PostgreSQL media-SSOT cutover (media demolition, September 2026); the Qdrant media collection is no longer a projection — the media index plane is PostgreSQL + pgvector (media_embeddings) and is rebuilt by pgmedia.PostgresIndexWorker, so use 'backfill-media-postgres' for media backfills")

// RunReconcileQdrant is retained as a subcommand stub so existing operator
// scripts fail LOUDLY with the typed retirement error instead of a silent
// command-not-found.
func RunReconcileQdrant(args []string) error {
	_, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()
	log.Warn("reconcile-qdrant invoked after media demolition", zap.Strings("args", args))
	return fmt.Errorf("%w", ErrMediaProjectionRetired)
}

// RunReindexQdrant is retained as a subcommand stub for the same reason as
// RunReconcileQdrant: the media rebuild no longer has a target projection, so
// the command must fail closed with a typed error rather than rebuild a
// retired collection from the quarantined SQLite media catalog.
func RunReindexQdrant(args []string) error {
	_, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()
	log.Warn("reindex-qdrant invoked after media demolition", zap.Strings("args", args))
	return fmt.Errorf("%w", ErrMediaReindexRetired)
}
