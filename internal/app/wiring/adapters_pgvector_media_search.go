// Package wiring — adapters_pgvector_media_search.go resolves the canonical
// media semantic-search plane at composition time.
//
// POSTGRES-MEDIA-CUTOVER (September 2026): PostgreSQL + pgvector is the only
// media-domain search authority. The same MediaSearcher instance implements
// both VectorStorePort and MediaReadRepository, so retrieval and hydration
// cannot drift across databases. Qdrant and SQLite are never media fallbacks.
package wiring

import (
	"database/sql"
	"fmt"

	"go.uber.org/zap"

	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

// selectMediaSearchStore is the single resolver for canonical semantic media
// reads. It consumes the PostgreSQL handle already opened by the composition
// root; it never creates a second pool. Disabled means the media search plane
// is intentionally absent. Enabled + nil handle is a hard composition error.
func selectMediaSearchStore(
	cfg *config.Config,
	pg *sql.DB,
	log *zap.Logger,
) (assetsearch.VectorStorePort, assetsearch.MediaReadRepository, bool, error) {
	if cfg == nil || !cfg.MediaPostgreSQL.Enabled {
		return nil, nil, false, nil
	}
	if pg == nil {
		return nil, nil, false, fmt.Errorf("media search store: PostgreSQL enabled but canonical media handle is nil")
	}
	if log == nil {
		log = zap.NewNop()
	}

	store := pgmedia.NewMediaSearcher(pg)
	log.Info("POSTGRES-MEDIA-CUTOVER: media semantic search resolved to one PostgreSQL MediaSearcher (pgvector + media_assets hydration)")
	return store, store, true, nil
}
