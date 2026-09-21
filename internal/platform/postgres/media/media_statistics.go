// Package media — media_statistics.go: the per-source media_assets count the
// Artlist diagnostics surface needs, on the media SSOT.
//
// WHY THIS EXISTS. internal/capabilities/assets/providers/artlist/diagnostics.go
// sourced ClipsArtlistTotal from assets.CountBySource — the operational SQLite
// ClipsRepository (internal/platform/sqlite/assets/imagesregistry/
// clips_statistics.go) — while PostgreSQL owned media_assets. That is the same
// split-brain the rest of the media read plane has been retiring: a clip
// committed by the canonical PostgreSQL committer was invisible to the count,
// and a stale pre-cutover row could be counted forever, on a surface
// (/api/artlist/diagnostics) whose whole purpose is to be believable. The
// register's own staleness pin is what made this the last read in its file.
//
// SEMANTICS PRESERVED FROM THE RETIRED STATEMENT, field for field:
//
//   - COUNT(*) over media_assets filtered by the `source` column (the retired
//     form was `SELECT COUNT(*) FROM media_assets WHERE source = ?`);
//   - NO soft-delete discount: a soft-deleted row is still counted. The retired
//     helper documented this deliberately — it is a true "indexed" metric, not
//     an "online" one — and callers wanting the latter compose the lifecycle
//     filter themselves. Dropping the discount here would silently change the
//     number the diagnostics endpoint reports;
//   - an empty source is a TYPED FAILURE (ErrEmptySource), never a
//     count-of-everything: godlike/07 no-fake-availability. The retired helper
//     returned the same sentinel, so the caller behaviour (a probe failure
//     rather than an over-reported total) is unchanged.
//
// WIDENED 2026-09-21 (MEDIA LEGACY READ-PLANE DEMOLITION, sub-wave B'): the
// reader now also answers the two aggregates the rest of the Artlist diagnostics
// surface needs — the non-soft-deleted total (CountClips) and the newest
// created_at for a term (LastUpdatedAtForTerm). They came off the operational
// SQLite store (imagesregistry/clip_list_queries.go) for the same reason the
// per-source count did: they summarise rows PostgreSQL owns. The header rule
// below still holds — these three statements are the media-aggregate surface and
// a fourth COUNT(*) of media_assets belongs here, not in a second reader.
//
// NOT a search, a duplicate-group scan or an eligibility read: this reader
// answers aggregate questions about media_assets and owns those SELECTs.
// Adding another COUNT(*)/MAX(*) of media_assets elsewhere is a godlike/06
// violation — fan in here.
package media

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ErrEmptySource is the typed sentinel CountBySource returns when the caller
// passes an empty source string. It mirrors the sentinel the retired SQLite
// helper returned (assets.ErrEmptySource, deleted with clips_statistics.go on
// 2026-09-21) so a caller that branched on errors.Is keeps working after the
// migration.
var ErrEmptySource = errors.New("media.CountBySource: source is required (godlike/07 — never silently over-report a count of everything)")

// ErrEmptyTerm is the typed sentinel LastUpdatedAtForTerm returns for an empty
// term. It is a HARDENING, not a preserved semantic: the retired SQLite
// statement (clip_list_queries.go) had no sentinel and an empty term would have
// become `tags LIKE '%%'`, i.e. it would have answered "the newest artlist row"
// for a caller that forgot to pass a term. The single production caller already
// guards the empty case (`if term != ""`), so no call in the tree changes
// behaviour — the sentinel only closes the shape.
var ErrEmptyTerm = errors.New("media.LastUpdatedAtForTerm: term is required (godlike/07 — never answer an all-rows aggregate for a missing term)")

// MediaStatisticsReader answers per-source media_assets counts from the
// PostgreSQL media SSOT.
type MediaStatisticsReader struct {
	db *sql.DB
}

// NewMediaStatisticsReader returns a reader bound to the PostgreSQL media
// database. The handle is required: a nil database is a programming error, not
// a degrade path (matching NewMediaDuplicateGroupReader and the rest of this
// package's readers).
func NewMediaStatisticsReader(db *sql.DB) *MediaStatisticsReader {
	if db == nil {
		panic("media.NewMediaStatisticsReader: db is required")
	}
	return &MediaStatisticsReader{db: db}
}

// DB exposes the underlying handle so callers can assert the reader lives on
// the media SSOT.
func (r *MediaStatisticsReader) DB() *sql.DB {
	if r == nil {
		return nil
	}
	return r.db
}

// CountBySource returns the number of media_assets rows whose `source` column
// matches source exactly.
//
// Returns ErrEmptySource when source == "" so the caller surfaces a probe
// failure instead of over-reporting "everything". Soft-deleted rows are
// counted (see the file header): this is the indexed-total metric, not the
// online-asset metric.
func (r *MediaStatisticsReader) CountBySource(ctx context.Context, source string) (int, error) {
	if r == nil {
		return 0, errors.New("media.CountBySource: nil reader")
	}
	if r.db == nil {
		return 0, errors.New("media.CountBySource: nil db handle (composition root forgot to wire the media handle?)")
	}
	if source == "" {
		return 0, ErrEmptySource
	}
	const query = `SELECT COUNT(*) FROM media_assets WHERE source = $1`
	var count int
	if err := r.db.QueryRowContext(ctx, query, source).Scan(&count); err != nil {
		return 0, fmt.Errorf("media.CountBySource(%s): %w", source, err)
	}
	return count, nil
}

// CountClips returns the number of media_assets rows that are not soft-deleted,
// across every source.
//
// SEMANTICS PRESERVED from the retired SQLite statement
// (`SELECT COUNT(*) FROM media_assets WHERE lifecycle_state != 'DELETED'`,
// imagesregistry/clip_list_queries.go, deleted 2026-09-21):
//
//   - the soft-delete discount IS applied here, unlike CountBySource. The two
//     methods answer different questions on purpose (CountBySource is the
//     indexed-total metric, CountClips the online one) and both file headers say
//     so; unifying them would silently change a number an operator script reads;
//   - the filter is the engine-neutral TEXT SoftDeleteFilter()
//     (`lifecycle_state != 'DELETED'`). PostgreSQL declares
//     `lifecycle_state TEXT NOT NULL DEFAULT 'ACTIVE'`, so the engine change
//     cannot introduce a NULL row that the SQLite predicate silently dropped —
//     the one place where the verbatim port would have been unfaithful.
func (r *MediaStatisticsReader) CountClips(ctx context.Context) (int, error) {
	if r == nil {
		return 0, errors.New("media.CountClips: nil reader")
	}
	if r.db == nil {
		return 0, errors.New("media.CountClips: nil db handle (composition root forgot to wire the media handle?)")
	}
	const query = `SELECT COUNT(*) FROM media_assets WHERE lifecycle_state != 'DELETED'`
	var count int
	if err := r.db.QueryRowContext(ctx, query).Scan(&count); err != nil {
		return 0, fmt.Errorf("media.CountClips: %w", err)
	}
	return count, nil
}

// LastUpdatedAtForTerm returns the newest created_at among artlist rows whose
// `tags` text contains term, or nil when nothing matches.
//
// SEMANTICS PRESERVED + THE ONE FORCED DIVERGENCE:
//
//   - the source is hard-coded to 'artlist' (the retired statement was
//     `WHERE source = 'artlist' AND tags LIKE ?`), so this can never drift into
//     an all-source answer;
//   - `created_at` is TEXT on BOTH schemas (the PostgreSQL DDL mirrors SQLite,
//     with created_at_ts as the typed twin the dual-write gate governs), so MAX()
//     over it is the same lexicographic-on-RFC3339 maximum the SQLite column
//     produced, and the value is returned verbatim as a *string to keep the
//     operator-facing format identical;
//   - **LIKE had to become ILIKE.** SQLite's LIKE is case-INsensitive for ASCII
//     and PostgreSQL's is case-sensitive, so a verbatim port would have silently
//     stopped matching a term against a differently-cased tag. Pinning the
//     case-insensitive behaviour on the real engine is exactly what the live test
//     below does — the same divergence, and the same reasoning, the operatorread
//     migration recorded on 2026-09-20.
//
// A term that matches nothing returns (nil, nil): the retired helper collapsed
// "no rows" and "empty column" into the same nil, and the diagnostics surface
// interprets nil as "no run yet" (godlike/07 honours a fresh install instead of
// fabricating a timestamp).
func (r *MediaStatisticsReader) LastUpdatedAtForTerm(ctx context.Context, term string) (*string, error) {
	if r == nil {
		return nil, errors.New("media.LastUpdatedAtForTerm: nil reader")
	}
	if r.db == nil {
		return nil, errors.New("media.LastUpdatedAtForTerm: nil db handle (composition root forgot to wire the media handle?)")
	}
	term = strings.TrimSpace(term)
	if term == "" {
		return nil, ErrEmptyTerm
	}
	const query = `
		SELECT COALESCE(MAX(created_at), '')
		FROM media_assets
		WHERE source = 'artlist' AND tags ILIKE $1`
	var lastUpdated string
	if err := r.db.QueryRowContext(ctx, query, "%"+term+"%").Scan(&lastUpdated); err != nil {
		return nil, fmt.Errorf("media.LastUpdatedAtForTerm(%s): %w", term, err)
	}
	if strings.TrimSpace(lastUpdated) == "" {
		return nil, nil
	}
	return &lastUpdated, nil
}
