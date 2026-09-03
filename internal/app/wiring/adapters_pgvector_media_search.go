// Package app — adapters_pgvector_media_search.go is the composition-root
// bridge that selects the media vector-search plane at boot.
//
// POSTGRES-MEDIA-CUTOVER: when cfg.MediaPostgreSQL.Enabled is true, the
// canonical search.VectorStorePort is implemented by the pgvector
// MediaSearcher over the same PostgreSQL SSOT that owns media_assets —
// Qdrant is NOT consulted for media reads or writes (AGENTS.md: Qdrant
// media reads and writes must remain disabled during and after cutover).
//
// godlike/07 fail-closed: when the media Postgres connection cannot be
// opened or lacks the vector surfaces, composition aborts with a typed
// error — never a silent downgrade to the SQLite/Qdrant plane.
package wiring

import (
	"context"
	"database/sql"
	"fmt"

	"go.uber.org/zap"

	mediasub "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/media"
	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

// selectMediaVectorStore resolves the canonical VectorStorePort for the
// semantic search plane. Returns (store, pgDB, true, nil) when the
// PostgreSQL media SSOT owns the vector plane; (nil, nil, false, nil)
// when the deployment is still SQLite/Qdrant-mode (unchanged behavior).
func selectMediaVectorStore(ctx context.Context, cfg *config.Config, log *zap.Logger) (assetsearch.VectorStorePort, *sql.DB, bool, error) {
	if cfg == nil || !cfg.MediaPostgreSQL.Enabled {
		return nil, nil, false, nil
	}
	if log == nil {
		log = zap.NewNop()
	}
	pg, err := mediasub.RequireMediaPostgres(ctx, cfg)
	if err != nil {
		return nil, nil, false, fmt.Errorf("media vector store: %w", err)
	}
	store := pgmedia.NewMediaSearcher(pg)
	log.Info("POSTGRES-MEDIA-CUTOVER: media vector search plane resolved to PostgreSQL + pgvector (Qdrant media reads disabled)")
	return store, pg, true, nil
}

// pgVectorStoreFrom constructs the canonical VectorStorePort from an open
// media Postgres handle. Split from selectMediaVectorStore so tests can
// pin the selection logic without a live DSN.
func pgVectorStoreFrom(pg *sql.DB) assetsearch.VectorStorePort {
	return pgmedia.NewMediaSearcher(pg)
}
