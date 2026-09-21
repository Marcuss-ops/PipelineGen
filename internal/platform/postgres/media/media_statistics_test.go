// Package media_test — media_statistics_test.go pins the per-source
// media_assets count the Artlist diagnostics surface reads
// (MEDIA LEGACY READ-PLANE DEMOLITION, 2026-09-21).
//
// Two layers, deliberately separated:
//
//   - the GUARD layer runs in the default hermetic `go test ./...`: the
//     constructor's nil-handle contract, the nil-receiver fail-closed path, and
//     the empty-source sentinel (checked before any query runs, so it needs a
//     handle but no server);
//   - the SEMANTICS layer is live-PostgreSQL, because the properties it pins
//     are the engine's: exact source match, cross-source exclusion, and the
//     deliberate absence of a soft-delete discount that the retired SQLite
//     helper (clips_statistics.go, assets.ErrEmptySource) documented. It uses
//     the package's `newMediaTestDB` fixture, which resets media_assets and
//     refuses any database that is not unambiguously a *_test one. Skipped,
//     never faked, when TEST_POSTGRES_DSN is unset (godlike/07).
package media_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

// TestMediaStatisticsReader_Guards covers the fail-closed layer that must hold
// without a database.
func TestMediaStatisticsReader_Guards(t *testing.T) {
	// The handle is required at construction: a nil database is a programming
	// error, not a degrade path (the convention this package's readers share), so
	// there is no way to build a reader that silently answers 0 for everything.
	require.Panics(t, func() { pgmedia.NewMediaStatisticsReader(nil) },
		"a nil media handle must panic at construction, never produce a reader that answers 0")

	// sql.Open is lazy — no connection is attempted until a query runs — which is
	// exactly what lets the empty-source guard be pinned without a server.
	db, err := sql.Open("pgx", "postgres://pipelinegen:pipelinegen@127.0.0.1:1/pipelinegen_media_test?sslmode=disable")
	require.NoError(t, err, "sql.Open must not dial")
	t.Cleanup(func() { _ = db.Close() })

	reader := pgmedia.NewMediaStatisticsReader(db)

	t.Run("nil receiver fails closed", func(t *testing.T) {
		var nilReader *pgmedia.MediaStatisticsReader
		count, err := nilReader.CountBySource(context.Background(), "artlist")
		require.Error(t, err, "nil receiver must return a typed error, not panic")
		require.Contains(t, err.Error(), "nil reader")
		assert.Equal(t, 0, count, "count must be 0 on the error path")
	})

	t.Run("empty source is a typed failure, never a count of everything", func(t *testing.T) {
		count, err := reader.CountBySource(context.Background(), "")
		require.ErrorIs(t, err, pgmedia.ErrEmptySource,
			"empty source must return the canonical sentinel the retired SQLite helper returned (godlike/07)")
		assert.Equal(t, 0, count, "count must be 0 on the error path")
	})

	t.Run("nil receiver fails closed for every aggregate", func(t *testing.T) {
		var nilReader *pgmedia.MediaStatisticsReader

		_, err := nilReader.CountClips(context.Background())
		require.Error(t, err, "nil receiver must return a typed error, not panic")
		require.Contains(t, err.Error(), "nil reader")

		_, err = nilReader.LastUpdatedAtForTerm(context.Background(), "beluga")
		require.Error(t, err, "nil receiver must return a typed error, not panic")
		require.Contains(t, err.Error(), "nil reader")
	})

	t.Run("empty term is a typed failure, never an all-rows aggregate", func(t *testing.T) {
		// HARDENING, not a preserved semantic: the retired SQLite statement had no
		// sentinel, so a forgotten term became `tags LIKE '%%'` and answered "the
		// newest artlist row". The only production caller already guards this, so the
		// sentinel closes the shape without changing any call in the tree.
		got, err := reader.LastUpdatedAtForTerm(context.Background(), "   ")
		require.ErrorIs(t, err, pgmedia.ErrEmptyTerm,
			"an empty term must be refused before the query runs (godlike/07)")
		assert.Nil(t, got, "no timestamp may be fabricated on the error path")
	})
}

// TestMediaStatisticsReader_ClipAggregatesOnTheSSOT pins the two aggregates that
// ported off the operational SQLite store in the same pass (CountClips and
// LastUpdatedAtForTerm, imagesregistry/clip_list_queries.go), including the
// forced LIKE -> ILIKE divergence.
func TestMediaStatisticsReader_ClipAggregatesOnTheSSOT(t *testing.T) {
	db := newMediaTestDB(t) // resets media_assets; refuses a non *_test database
	ctx := context.Background()
	reader := pgmedia.NewMediaStatisticsReader(db)

	// The fixture is built so each pin is falsifiable on its own:
	//   - agg-artlist-del is the NEWEST artlist row (2026-09-18) and is soft-deleted,
	//     so a lifecycle discount would move the answer down to 2026-09-17;
	//   - agg-stock is the newest row OVERALL (2026-09-19) with the same tag, so a
	//     port that dropped `source = 'artlist'` would return it;
	//   - no tag contains the lower-case string "beluga", so a case-SENSITIVE LIKE
	//     would find nothing at all.
	const (
		olderArtlist = "2026-09-10T08:00:00Z"
		newerArtlist = "2026-09-17T09:30:00Z"
	)
	seed := []struct{ id, source, lifecycle, createdAt, tags string }{
		{"agg-artlist-old", "artlist", "ACTIVE", olderArtlist, `["Beluga underwater"]`},
		{"agg-artlist-new", "artlist", "ACTIVE", newerArtlist, `["Beluga underwater","Round-trip Search Clip Alpha"]`},
		{"agg-artlist-del", "artlist", "DELETED", "2026-09-18T10:00:00Z", `["Beluga underwater"]`},
		{"agg-stock", "stock", "ACTIVE", "2026-09-19T11:00:00Z", `["Beluga underwater"]`},
	}
	for _, row := range seed {
		_, err := db.ExecContext(ctx, `
			INSERT INTO media_assets (id, source, lifecycle_state, created_at, updated_at, tags)
			VALUES ($1, $2, $3, $4, $4, $5)`,
			row.id, row.source, row.lifecycle, row.createdAt, row.tags)
		require.NoError(t, err, "seed %s", row.id)
	}

	t.Run("CountClips discounts soft-deleted rows across sources", func(t *testing.T) {
		got, err := reader.CountClips(ctx)
		require.NoError(t, err)
		assert.Equal(t, 3, got,
			"4 seeded rows minus the DELETED one: the soft-delete discount IS applied here (unlike CountBySource, which counts indexed rows)")
	})

	t.Run("the term match is case-insensitive (ILIKE, the forced divergence)", func(t *testing.T) {
		got, err := reader.LastUpdatedAtForTerm(ctx, "beluga")
		require.NoError(t, err)
		require.NotNil(t, got,
			"no stored tag contains the lower-case string \"beluga\", so a case-SENSITIVE LIKE would have returned nil here")
		assert.Equal(t, "2026-09-18T10:00:00Z", *got,
			"the newest ARTlist match wins, and the DELETED row still participates: the retired statement had no lifecycle filter (preserved quirk, pinned so a future change is deliberate)")
	})

	t.Run("the source filter keeps the newest stock row out", func(t *testing.T) {
		got, err := reader.LastUpdatedAtForTerm(ctx, "Beluga")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.NotEqual(t, "2026-09-19T11:00:00Z", *got,
			"agg-stock carries the same tag with the newest created_at overall; dropping `source = 'artlist'` would have returned it")
	})

	t.Run("a term that matches nothing is (nil, nil)", func(t *testing.T) {
		got, err := reader.LastUpdatedAtForTerm(ctx, "no-such-term-anywhere")
		require.NoError(t, err, "an unmatched term is the canonical happy-path nil, not an error")
		assert.Nil(t, got, "nil is the retired helper's contract: the diagnostics surface reads it as \"no run yet\"")
	})

	t.Run("empty table answers 0 and nil", func(t *testing.T) {
		_, err := db.ExecContext(ctx, `DELETE FROM media_assets`)
		require.NoError(t, err)

		count, err := reader.CountClips(ctx)
		require.NoError(t, err)
		assert.Equal(t, 0, count, "a fresh install must report 0, not an error")

		got, err := reader.LastUpdatedAtForTerm(ctx, "beluga")
		require.NoError(t, err)
		assert.Nil(t, got, "no rows = nil, never a fabricated timestamp")
	})
}

// TestMediaStatisticsReader_CountsPerSourceOnTheSSOT ports the properties the
// retired SQLite helper certified against its own fixture (a 3 + 2 + 1 row
// window), on the engine that owns media_assets.
func TestMediaStatisticsReader_CountsPerSourceOnTheSSOT(t *testing.T) {
	db := newMediaTestDB(t) // resets media_assets; refuses a non *_test database
	ctx := context.Background()
	reader := pgmedia.NewMediaStatisticsReader(db)

	now := time.Now().UTC().Format(time.RFC3339)
	seed := []struct{ id, source, lifecycle string }{
		{"stats-artlist-1", "artlist", "ACTIVE"},
		{"stats-artlist-2", "artlist", "ACTIVE"},
		{"stats-artlist-3", "artlist", "DELETED"},
		{"stats-stock-1", "stock", "ACTIVE"},
	}
	for _, row := range seed {
		_, err := db.ExecContext(ctx, `
			INSERT INTO media_assets (id, source, lifecycle_state, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $4)`,
			row.id, row.source, row.lifecycle, now)
		require.NoError(t, err, "seed %s", row.id)
	}

	t.Run("counts the matching source exactly", func(t *testing.T) {
		artlist, err := reader.CountBySource(ctx, "artlist")
		require.NoError(t, err)
		assert.Equal(t, 3, artlist, "3 artlist rows; the stock row must be excluded by the source filter")

		stock, err := reader.CountBySource(ctx, "stock")
		require.NoError(t, err)
		assert.Equal(t, 1, stock, "1 stock row; the three artlist rows must be excluded")
	})

	t.Run("soft-deleted rows are still counted", func(t *testing.T) {
		// The DELETED row above is part of the 3. This is the retired helper's
		// documented contract (an "indexed" metric, not an "online" one) and
		// dropping it here would silently change the number /api/artlist/diagnostics
		// reports.
		artlist, err := reader.CountBySource(ctx, "artlist")
		require.NoError(t, err)
		assert.Equal(t, 3, artlist,
			"a DELETED lifecycle_state must NOT be discounted: the metric counts indexed rows, not online rows")
	})

	t.Run("absent source is 0, not an error", func(t *testing.T) {
		count, err := reader.CountBySource(ctx, "no-such-source")
		require.NoError(t, err, "a valid source with no rows is the canonical happy-path 0")
		assert.Equal(t, 0, count, "no rows = 0 (NOT an error)")
	})

	t.Run("empty source is refused before the query", func(t *testing.T) {
		count, err := reader.CountBySource(ctx, "")
		require.ErrorIs(t, err, pgmedia.ErrEmptySource,
			"the guard must run on the live handle too, so an empty source can never over-report the whole table")
		assert.Equal(t, 0, count)
	})
}
