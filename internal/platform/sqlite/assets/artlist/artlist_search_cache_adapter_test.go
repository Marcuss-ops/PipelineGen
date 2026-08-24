// Package sqlite — artlist_search_cache_adapter_test.go: hermetic
// regression lock for the typed SQLite adapter of the Artlist
// persistent search cache (PR-P0-3, July 2026).
//
// Tests run against an in-memory SQLite database with the canonical
// artlist_search_cache table created inline; no external fixtures,
// no Qdrant, no Drive. Mirrors the artlist_runs_repository_test.go
// hermetic pattern.
package artlist

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/artlist"
)

const artlistSearchCacheTableDDL = `
CREATE TABLE IF NOT EXISTS artlist_search_cache (
    term       TEXT PRIMARY KEY,
    clips_json TEXT NOT NULL,
    cached_at  TEXT NOT NULL
)
`

// openInMemoryCacheDB returns a fresh :memory: SQLite with the
// canonical table created. Each test calls this independently so
// tests cannot leak state across runs.
func openInMemoryCacheDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(artlistSearchCacheTableDDL)
	require.NoError(t, err)
	return db
}

func TestSQLiteArtlistSearchCacheAdapter_Warm_EmptyTable(t *testing.T) {
	db := openInMemoryCacheDB(t)
	port := NewSQLiteArtlistSearchCacheAdapter(db, zap.NewNop())
	ents, err := port.Warm(context.Background())
	require.NoError(t, err)
	assert.Empty(t, ents, "warm on empty table returns nil slice (no error)")
}

func TestSQLiteArtlistSearchCacheAdapter_SetThenGet(t *testing.T) {
	db := openInMemoryCacheDB(t)
	port := NewSQLiteArtlistSearchCacheAdapter(db, zap.NewNop())
	clips := []artlist.Candidate{{ID: "clip-1", Title: "T"}, {ID: "clip-2", Title: "U"}}
	ctx := context.Background()
	require.NoError(t, port.Set(ctx, "term-1", clips))
	got, _, ok, err := port.Get(ctx, "term-1")
	require.NoError(t, err)
	require.True(t, ok, "Get after Set MUST return ok=true")
	assert.Equal(t, clips, got)
}

func TestSQLiteArtlistSearchCacheAdapter_SetUpsertsOnConflict(t *testing.T) {
	db := openInMemoryCacheDB(t)
	port := NewSQLiteArtlistSearchCacheAdapter(db, zap.NewNop())
	ctx := context.Background()
	first := []artlist.Candidate{{ID: "v1"}}
	second := []artlist.Candidate{{ID: "v2"}}
	require.NoError(t, port.Set(ctx, "term-1", first))
	require.NoError(t, port.Set(ctx, "term-1", second))
	got, _, ok, err := port.Get(ctx, "term-1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, got, 1)
	assert.Equal(t, "v2", got[0].ID, "ON CONFLICT term MUST update the row (set replaces set)")
}

func TestSQLiteArtlistSearchCacheAdapter_GetMissReturnsFalseNil(t *testing.T) {
	db := openInMemoryCacheDB(t)
	port := NewSQLiteArtlistSearchCacheAdapter(db, zap.NewNop())
	got, _, ok, err := port.Get(context.Background(), "missing-term")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Nil(t, got, "miss MUST return nil clips; caller branches on boolean not nil-slice")
}

func TestSQLiteArtlistSearchCacheAdapter_Delete(t *testing.T) {
	db := openInMemoryCacheDB(t)
	port := NewSQLiteArtlistSearchCacheAdapter(db, zap.NewNop())
	ctx := context.Background()
	require.NoError(t, port.Set(ctx, "term-1", []artlist.Candidate{{ID: "x"}}))
	require.NoError(t, port.Delete(ctx, "term-1"))
	_, _, ok, err := port.Get(ctx, "term-1")
	require.NoError(t, err)
	assert.False(t, ok, "post-Delete Get MUST return ok=false")
}

func TestSQLiteArtlistSearchCacheAdapter_GetExpiredDeletesRowAndReturnsMiss(t *testing.T) {
	db := openInMemoryCacheDB(t)
	port := NewSQLiteArtlistSearchCacheAdapter(db, zap.NewNop())
	ctx := context.Background()
	require.NoError(t, port.Set(ctx, "term-old", []artlist.Candidate{{ID: "old"}}))
	// Backdate the row past the 48h hard limit. We re-write
	// the cached_at column directly because the production
	// code only allows datetime('now') — the only way to test
	// the expired-cleanup path is to bypass the production
	// timestamper.
	_, err := db.ExecContext(ctx,
		`UPDATE artlist_search_cache SET cached_at = datetime('now', '-50 hours') WHERE term = ?`,
		"term-old",
	)
	require.NoError(t, err)
	got, _, ok, err := port.Get(ctx, "term-old")
	require.NoError(t, err)
	assert.False(t, ok, "expired entry MUST be deleted in-line and returned as a miss")
	assert.Nil(t, got)
	// Verify the row is gone.
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM artlist_search_cache WHERE term = ?`, "term-old").Scan(&count))
	assert.Equal(t, 0, count, "expired entry MUST be hard-deleted")
}

func TestSQLiteArtlistSearchCacheAdapter_CleanupExpired(t *testing.T) {
	db := openInMemoryCacheDB(t)
	port := NewSQLiteArtlistSearchCacheAdapter(db, zap.NewNop())
	ctx := context.Background()
	for _, term := range []string{"fresh", "old"} {
		require.NoError(t, port.Set(ctx, term, []artlist.Candidate{{ID: term}}))
	}
	_, err := db.ExecContext(ctx,
		`UPDATE artlist_search_cache SET cached_at = datetime('now', '-50 hours') WHERE term = ?`,
		"old",
	)
	require.NoError(t, err)
	require.NoError(t, port.CleanupExpired(ctx, time.Minute))
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM artlist_search_cache`).Scan(&count))
	assert.Equal(t, 1, count, "CleanupExpired MUST delete only the row past the 48h hard limit")
}
