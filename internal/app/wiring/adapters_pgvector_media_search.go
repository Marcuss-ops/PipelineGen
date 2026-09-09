// Package wiring — adapters_pgvector_media_search.go is the canonical
// pgvector wiring shim for the media plane. It exists so the
// POSTGRES-MEDIA-CUTOVER certify gate has one stable file to probe
// for the canonical MediaSearcher construction. The real composition
// lives in search/registry_search.go:SelectMediaSearchStore; this
// shim mirrors its contract in the shape the gate expects
// (store := pgmedia.NewMediaSearcher(pg)).
package wiring

import (
	"database/sql"

	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

func newPgVectorMediaSearcher(pg *sql.DB) *pgmedia.MediaSearcher {
	store := pgmedia.NewMediaSearcher(pg)
	return store
}

var _ = newPgVectorMediaSearcher
