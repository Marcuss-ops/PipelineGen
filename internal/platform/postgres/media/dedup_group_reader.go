// Package media — dedup_group_reader.go: the two media_assets reads the
// clip-dedup sweeper needs, on the media SSOT.
//
// WHY THIS EXISTS. internal/app/wiring/lifecycle_sweepers.go::runDedupSweep
// used to reach media_assets through the OPERATIONAL handle
// (*imagesregistry.ClipsRepository, i.e. `dbs.Main`) while PostgreSQL owned the
// table:
//
//	SELECT json_extract(metadata_json,'$.youtube_video_id') ... FROM media_assets
//	INSERT/UPDATE ... WHERE id = ?        (DeleteClip -> SoftDelete)
//
// Two consequences, and the second is the one that made this a P2-9 read-debt
// entry rather than a cosmetic engine swap:
//
//  1. The scan enumerated duplicates on the mirror. A duplicate pair committed
//     by the canonical writer could be invisible to the sweep, so the sweep
//     silently under-reported.
//  2. Worse, once that scan DID find a pair (because the mirror happened to
//     hold one), the retirement landed on the mirror too — so the duplicate
//     stayed live on the SSOT and the sweep would find it again on the next
//     tick, forever. That is why this site could not be migrated read-first:
//     a migrated read with an unmigrated write is worse than no migration, and
//     both halves are therefore answered from one engine by construction (see
//     wiring.mediaDuplicateGroupReaderFromCommitter and
//     persistence.CanonicalAssetSoftDeleter, both resolved from the same
//     committer).
//
// Semantics are preserved from the retired SQLite statements, field for field:
//
//   - only rows whose lifecycle_state is not 'DELETED' participate (the retired
//     form interpolated detail.SoftDeleteFilter(), literally
//     `lifecycle_state != 'DELETED'`). The PostgreSQL column is NOT NULL, so
//     `<>` and `!=` agree on every row — a NULL row, which SQL three-valued
//     logic would have excluded on SQLite, cannot exist here;
//   - a row whose metadata_json carries no youtube_video_id (absent, JSON null,
//     or empty string) never participates;
//   - groups are limited to those with more than one member;
//   - the per-group id list excludes the caller's own id and is ordered by
//     created_at DESC, so index 0 is the NEWEST survivor and the caller retires
//     from index 1 — the newest row is the one kept.
//
// Two deliberate differences, both because the retired form was not total. The
// group query is now ORDER BY-ed: SQLite's `GROUP BY vid HAVING n > 1 LIMIT 500`
// had no ordering, so which 500 groups a truncated sweep visited was
// unspecified. And the id list breaks created_at ties on id: SQLite's
// `ORDER BY created_at DESC` alone left equal-timestamp rows unordered, which
// made "which copy survives" unspecified precisely in the case the sweeper
// exists to resolve. Both changes replace unspecified behaviour with
// deterministic behaviour; neither invents a new filter.
//
// Malformed JSON is NOT newly tolerated: SQLite's json_extract raises on it
// too, so an unparseable metadata_json fails the sweep on both engines. The
// cast is guarded only against the empty string, which the PostgreSQL column
// uses as its NOT NULL default and which is not valid JSON.
package media

import (
	"context"
	"database/sql"
	"fmt"
)

// YouTubeIDDuplicateGroup is one youtube_video_id shared by more than one live
// media asset.
type YouTubeIDDuplicateGroup struct {
	YouTubeVideoID string
	Count          int
}

// MediaDuplicateGroupReader answers the clip-dedup sweeper's media_assets reads
// from the PostgreSQL media SSOT.
type MediaDuplicateGroupReader struct {
	db *sql.DB
}

// NewMediaDuplicateGroupReader returns a reader bound to the PostgreSQL media
// database. The handle is required: a nil database is a programming error, not
// a degrade path.
func NewMediaDuplicateGroupReader(db *sql.DB) *MediaDuplicateGroupReader {
	if db == nil {
		panic("media.NewMediaDuplicateGroupReader: db is required")
	}
	return &MediaDuplicateGroupReader{db: db}
}

// DB exposes the underlying handle so callers can assert the reader lives on
// the media SSOT.
func (r *MediaDuplicateGroupReader) DB() *sql.DB {
	if r == nil {
		return nil
	}
	return r.db
}

// youtubeIDExpr is the canonical projection of the youtube_video_id metadata
// key. It is repeated rather than parameterised because PostgreSQL allows a
// parameter exactly once per execution; the three occurrences are the same
// expression by construction, and the live-PG pin
// (TestMediaDuplicateGroupReader_MatchesLegacySQLiteSemantics) fails if they
// ever drift apart.
const youtubeIDExpr = `(NULLIF(metadata_json, '')::jsonb)->>'youtube_video_id'`

// DuplicateYouTubeIDGroups returns up to limit youtube_video_id groups holding
// more than one live media asset. A limit <= 0 means no limit.
//
// An empty result is a normal case (no duplicates), not an error.
func (r *MediaDuplicateGroupReader) DuplicateYouTubeIDGroups(ctx context.Context, limit int) ([]YouTubeIDDuplicateGroup, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("media duplicate group reader: media SSOT handle is not configured")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	query := `
		SELECT ` + youtubeIDExpr + ` AS vid, COUNT(*) AS n
		FROM media_assets
		WHERE lifecycle_state <> 'DELETED'
		  AND ` + youtubeIDExpr + ` IS NOT NULL
		  AND ` + youtubeIDExpr + ` <> ''
		GROUP BY 1
		HAVING COUNT(*) > 1
		ORDER BY 1`
	args := []any{}
	if limit > 0 {
		query += ` LIMIT $1`
		args = append(args, limit)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("media duplicate group reader: duplicate group query: %w", err)
	}
	defer rows.Close()

	var groups []YouTubeIDDuplicateGroup
	for rows.Next() {
		var g YouTubeIDDuplicateGroup
		if err := rows.Scan(&g.YouTubeVideoID, &g.Count); err != nil {
			return nil, fmt.Errorf("media duplicate group reader: duplicate group scan: %w", err)
		}
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("media duplicate group reader: duplicate group rows: %w", err)
	}
	return groups, nil
}

// DuplicateAssetIDsByYouTubeID returns the ids of the live media assets that
// share videoID, newest first, excluding excludeID.
//
// Ordering is load-bearing, not cosmetic: the sweeper keeps the FIRST id and
// retires the rest, so the ordering is what makes "keep the newest copy" true.
func (r *MediaDuplicateGroupReader) DuplicateAssetIDsByYouTubeID(ctx context.Context, videoID, excludeID string) ([]string, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("media duplicate group reader: media SSOT handle is not configured")
	}
	if videoID == "" {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id FROM media_assets
		WHERE `+youtubeIDExpr+` = $1
		  AND lifecycle_state <> 'DELETED'
		  AND id <> $2
		ORDER BY created_at DESC, id DESC
	`, videoID, excludeID)
	if err != nil {
		return nil, fmt.Errorf("media duplicate group reader: duplicate id query: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("media duplicate group reader: duplicate id scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("media duplicate group reader: duplicate id rows: %w", err)
	}
	return ids, nil
}
