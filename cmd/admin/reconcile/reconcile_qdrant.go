// Package reconcile — reconcile_qdrant.go (RETIRED, media demolition
// September 2026).
//
// The QDRANT-005B media_assets reconciler compared the SQLite media
// catalog against the Qdrant media_assets projection and dispatched
// outbox repairs. With the PostgreSQL + pgvector media SSOT certified
// (POSTGRES_MEDIA_SSOT=TRUE) the SQLite → Qdrant media projection chain
// no longer exists: SQLite media writes are demolished and the media
// vector plane lives inside the PostgreSQL SSOT (media_embeddings), where
// parity is enforced by the transactional commit path itself — there is
// nothing left to reconcile.
//
// `reindex-qdrant` (RunReindexQdrant) remains operational for the
// non-media Qdrant collections (mediamemory frames, clip channels) that
// are outside the media demolition scope.
package reconcile

import (
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"
	"go.uber.org/zap"
)

// ErrMediaProjectionRetired is returned by the retired media reconciler.
var ErrMediaProjectionRetired = errors.New("reconcile-qdrant: the SQLite→Qdrant media_assets projection was retired with the PostgreSQL media-SSOT cutover (media demolition, September 2026); media parity is enforced transactionally inside PostgreSQL — use 'backfill-media-postgres --verify-only' for media parity evidence")

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
