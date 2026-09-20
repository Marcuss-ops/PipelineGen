// Package app — canonical_media_committer_derived_reads.go holds the family of
// read adapters DERIVED FROM the canonical media committer's own engine.
//
// WHY THE WHOLE FAMILY LIVES IN ONE FILE. Every helper below answers the same
// question — "given the committer that owns the media writes, which engine does
// this read run on?" — and every one of them answers it the same way: nil
// committer, or a committer with no PostgreSQL handle, yields nil so the caller
// fails closed instead of degrading onto the operational SQLite mirror. Keeping
// them together makes that single decision reviewable as one unit; splitting
// them across the composition root is how a helper quietly grows a SQLite
// fallback that the media SSOT cutover removed.
//
// PostgreSQL is the ONLY media read authority (see the package comment in
// canonical_media_committer.go). There is no fallback here by design.
package wiring

import (
	"database/sql"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

// mediaDetailsReaderFromCommitter resolves the media-details read the ingest
// registries hydrate their media records from, using the engine of the
// canonical committer.
//
// WHY THE COMMITTER AND NOT A SECOND HANDLE. The ingest lifecycle stores and
// the ClipsRegistry hydrate a media record before staging/publishing it. When
// they read that record from the operational SQLite mirror while the canonical
// committer writes PostgreSQL, the two disagree: a clip committed by the
// canonical writer looks absent to the ingest path, and a stale pre-cutover
// row can be resurrected. Deriving the read from the committer's own engine
// makes the read and the write structurally unable to drift onto two engines
// (same invariant as MediaRepoBundle, which derives its Reader from the handle
// its Writer uses).
//
// A nil result means the media plane is closed; the returned concrete type
// fails closed with a typed error on use rather than falling back to SQLite.
func mediaDetailsReaderFromCommitter(committer persistence.AssetCommitter) *pgmedia.AssetDetailsReader {
	if committer == nil {
		return nil
	}
	getter, ok := committer.(interface{ DB() *sql.DB })
	if !ok || getter == nil {
		return nil
	}
	db := getter.DB()
	if db == nil {
		return nil
	}
	return pgmedia.NewAssetDetailsReader(pgmedia.NewMediaSearcher(db))
}

// mediaDriveFileListerFromCommitter resolves the "which assets still have a
// Drive file id" listing from the canonical committer's own engine.
//
// MEDIA-SSOT P2-9 Phase 2: internal/capabilities/assets/ingest previously ran
// this listing as raw SQL (`SELECT id FROM media_assets WHERE drive_file_id ...`)
// against the operational SQLite handle it was handed as `db`. PostgreSQL is
// the media SSOT, so that read could only ever see an empty catalog — a Drive
// sweep built on it was blind to every committed asset. This helper is the
// single owner of the engine decision, exactly like
// mediaDetailsReaderFromCommitter above: the read resolves from the committer
// that owns the media writes, so the two cannot drift onto different engines.
//
// nil means the media plane is closed. The caller MUST pass nil through and let
// the ingest listing fail closed; there is no SQLite fallback by design.
func mediaDriveFileListerFromCommitter(committer persistence.AssetCommitter) *pgmedia.MediaDriveFileLister {
	if committer == nil {
		return nil
	}
	getter, ok := committer.(interface{ DB() *sql.DB })
	if !ok || getter == nil {
		return nil
	}
	db := getter.DB()
	if db == nil {
		return nil
	}
	return pgmedia.NewMediaDriveFileLister(db)
}

// mediaYouTubeClipListerFromCommitter resolves the search_text rebuild job's
// "which YouTube clips carry a title" listing from the canonical committer's own
// engine.
//
// MEDIA-SSOT P2-9 Phase 2: internal/capabilities/youtube/adapters previously ran
// this listing as raw SQL (json_extract on media_assets) against the operational
// SQLite handle. PostgreSQL is the media SSOT, so that read could only ever see
// an empty catalog once the canonical writer held the rows — the rebuild job
// silently found nothing to rebuild. This helper is the single owner of the
// engine decision, exactly like mediaDetailsReaderFromCommitter and
// mediaDriveFileListerFromCommitter above.
//
// nil means the media plane is closed. The caller MUST pass nil through and the
// rebuild handler is then simply not registered; there is no SQLite fallback by
// design.
func mediaYouTubeClipListerFromCommitter(committer persistence.AssetCommitter) *pgmedia.MediaYouTubeClipLister {
	if committer == nil {
		return nil
	}
	getter, ok := committer.(interface{ DB() *sql.DB })
	if !ok || getter == nil {
		return nil
	}
	db := getter.DB()
	if db == nil {
		return nil
	}
	return pgmedia.NewMediaYouTubeClipLister(db)
}

// enrichStateStoreFromCommitter resolves the media_assets.enrich_state
// transition primitive from the canonical committer's own engine.
//
// WHY THIS DECISION LIVES HERE. The enrichment state machine was constructed
// with *ClipsRepository — the OPERATIONAL SQLite store — so every
// PENDING→ENRICHING→ENRICHED/FAILED transition landed on a mirror while
// pgmedia's own patch path wrote enrich_state on the SSOT: one fact, two
// writers, two engines. media_assets is owned by PostgreSQL, so the transitions
// resolve from the committer that owns the media writes, exactly like
// mediaDetailsReaderFromCommitter and mediaDriveFileListerFromCommitter above.
//
// The store also completes the migration-004 dual-write for
// enrich_state_updated_at, which the pair list already declared but no writer
// satisfied on the media SSOT — the VLM sweeper's claim fence depends on it.
//
// nil means the media plane is closed. The caller MUST pass nil through (the
// state machine is then simply not wired, and the sweeper fails closed) rather
// than falling back to the SQLite repository: that fallback is precisely the
// split-brain this removes.
func enrichStateStoreFromCommitter(committer persistence.AssetCommitter) *pgmedia.MediaEnrichStateStore {
	if committer == nil {
		return nil
	}
	getter, ok := committer.(interface{ DB() *sql.DB })
	if !ok || getter == nil {
		return nil
	}
	db := getter.DB()
	if db == nil {
		return nil
	}
	return pgmedia.NewMediaEnrichStateStore(db)
}

// mediaDuplicateGroupReaderFromCommitter resolves the clip-dedup sweeper's
// media_assets scan from the canonical committer's own engine.
//
// WHY THIS DECISION LIVES HERE. runDedupSweep used to scan media_assets through
// the OPERATIONAL SQLite handle (*imagesregistry.ClipsRepository) and then
// retire the duplicates it found on that same handle. The two halves of that
// sweep were therefore correct only while the mirror happened to agree with the
// SSOT: a duplicate pair committed by the canonical writer was invisible to the
// scan, and a pair the scan did find was retired on the mirror, so the SSOT copy
// stayed live and the sweeper counted the same duplicate on every tick.
//
// Deriving BOTH halves from one committer is the fix, and it is deliberately a
// single expression rather than two independent ones: the reader and the
// persistence.CanonicalAssetSoftDeleter resolved next to it must name the same
// engine, or the permanent-duplicate failure mode returns in a new shape.
//
// nil means the media plane is closed. The caller MUST skip the sweep and say
// so, rather than falling back to the SQLite repository — a sweep that retires
// assets on an engine nobody owns is worse than a sweep that does not run.
func mediaDuplicateGroupReaderFromCommitter(committer persistence.AssetCommitter) *pgmedia.MediaDuplicateGroupReader {
	if committer == nil {
		return nil
	}
	getter, ok := committer.(interface{ DB() *sql.DB })
	if !ok || getter == nil {
		return nil
	}
	db := getter.DB()
	if db == nil {
		return nil
	}
	return pgmedia.NewMediaDuplicateGroupReader(db)
}
