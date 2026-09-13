// Package maintenance — unify_catalogs.go (RETIRED, POSTGRES-MEDIA-CUTOVER,
// September 2026).
//
// RunUnifyCatalogs merged the legacy per-source SQLite catalogs
// (data/stock/stock.db.sqlite, data/artlist/artlist.db.sqlite) INTO
// media.db.sqlite `media_assets` with `INSERT OR IGNORE`.
//
// After the media cutover that write is a second, non-authoritative media
// catalog: PostgreSQL + pgvector is the SOLE durable authority for
// media_assets, and the canonical backfill path is
// `backfill-media-postgres`. Reintroducing a SQLite media writer is banned by
// `percheck_media_assets_writer_canonical` (the gate scans `cmd/` as well, see
// cmd/archcheck/scan/boundaries/percheck_media_assets_writer_canonical.go).
//
// The command therefore stays REGISTERED and fails LOUDLY with a typed
// retirement error, so existing operator scripts surface the retirement
// instead of a silent command-not-found — and so nobody believes a legacy
// catalog was merged into an authoritative store that it never touched.
package maintenance

import (
	"errors"
	"fmt"
)

// ErrUnifyCatalogsRetired is returned by the retired catalog unifier.
var ErrUnifyCatalogsRetired = errors.New("unify-catalogs: merging legacy per-source SQLite catalogs into media.db.sqlite was retired with the PostgreSQL media-SSOT cutover (media demolition, September 2026) — media_assets is authoritative ONLY in PostgreSQL + pgvector, and the canonical backfill is 'backfill-media-postgres'")

// RunUnifyCatalogs is retained as a subcommand stub: it fails closed with the
// typed retirement error instead of writing a second media catalog.
func RunUnifyCatalogs(_ []string) error {
	return fmt.Errorf("%w", ErrUnifyCatalogsRetired)
}
