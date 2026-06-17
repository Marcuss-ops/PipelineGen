package outbox

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// newInMemoryRepository spins up a SQLite ":memory:" database, applies the
// minimal media_index_outbox schema, and returns a Repository bound to it.
// The schema mirrors migrations/sqlite/023_media_index_outbox.sql so the
// CountByStatus predicate (`status` column) matches production.
func newInMemoryRepository(t *testing.T) *Repository {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
		CREATE TABLE media_index_outbox (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			asset_id TEXT NOT NULL,
			content_hash TEXT NOT NULL,
			embedding_model TEXT NOT NULL,
			embedding_version TEXT NOT NULL,
			collection_version TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			payload_json TEXT NOT NULL DEFAULT '',
			attempt_count INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT '',
			next_attempt_at TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			UNIQUE (asset_id, content_hash, embedding_model, embedding_version, collection_version)
		)
	`)
	require.NoError(t, err)
	return NewRepository(db, zap.NewNop())
}

// seedOutbox inserts a single row with the supplied status. asset_id is
// varied per row so the UNIQUE constraint doesn't reject duplicates.
func seedOutbox(t *testing.T, repo *Repository, assetID, status string) {
	t.Helper()
	_, err := repo.db.Exec(
		`INSERT INTO media_index_outbox (asset_id, content_hash, embedding_model, embedding_version, collection_version, status)
		 VALUES (?, 'hash', 'model', 'v1', 'collection', ?)`,
		assetID, status,
	)
	require.NoError(t, err)
}

func TestRepository_CountByStatus_BucketsAreIsolated(t *testing.T) {
	repo := newInMemoryRepository(t)
	ctx := context.Background()

	seedOutbox(t, repo, "pending_1", "pending")
	seedOutbox(t, repo, "pending_2", "pending")
	seedOutbox(t, repo, "in_flight_1", "in_flight")
	seedOutbox(t, repo, "processed_1", "processed")
	seedOutbox(t, repo, "dead_letter_1", "dead_letter")

	cases := []struct {
		status string
		want   int64
	}{
		{"pending", 2},
		{"in_flight", 1},
		{"processed", 1},
		{"dead_letter", 1},
	}
	for _, c := range cases {
		got, err := repo.CountByStatus(ctx, c.status)
		require.NoError(t, err)
		assert.Equal(t, c.want, got, "status=%q", c.status)
	}
}

func TestRepository_CountByStatus_EmptyStatusRejected(t *testing.T) {
	repo := newInMemoryRepository(t)
	_, err := repo.CountByStatus(context.Background(), "")
	assert.Error(t, err, "empty status must be rejected")
}

func TestRepository_CountByStatus_NilDB(t *testing.T) {
	repo := NewRepository(nil, zap.NewNop())
	_, err := repo.CountByStatus(context.Background(), "pending")
	assert.Error(t, err)
}

func TestRepository_CountByStatus_UnknownStatusReturnsZero(t *testing.T) {
	repo := newInMemoryRepository(t)
	got, err := repo.CountByStatus(context.Background(), "intentional_typo")
	require.NoError(t, err)
	assert.Equal(t, int64(0), got, "unknown status must return 0 not error — matches GROUP BY semantics")
}
