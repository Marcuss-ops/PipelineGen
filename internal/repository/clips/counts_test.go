package clips

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
// minimal media_assets schema + indexes, and returns a Repository bound to it.
// Tests in this file use it to exercise the PR3-5b counter / sampler methods
// without standing up the full migrations.
func newInMemoryRepository(t *testing.T) *Repository {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
		CREATE TABLE media_assets (
			id TEXT PRIMARY KEY,
			source TEXT,
			name TEXT,
			embedding_json TEXT,
			metadata_json TEXT
		)
	`)
	require.NoError(t, err)
	return NewRepository(db, zap.NewNop())
}

// seedAsset inserts one row; embeddingJSON is the literal string written to
// the embedding_json column (pass "" or "[]" for "not indexed").
func seedAsset(t *testing.T, repo *Repository, id, embeddingJSON, deletedAtJSON string) {
	t.Helper()
	metadata := "{}"
	if deletedAtJSON != "" {
		// Embed the deleted_at sentinel inside metadata_json so the json_extract
		// filter recognises the row as soft-deleted.
		metadata = `{"deleted_at":"` + deletedAtJSON + `"}`
	}
	_, err := repo.db.Exec(
		`INSERT INTO media_assets (id, source, name, embedding_json, metadata_json) VALUES (?,?,?,?,?)`,
		id, "youtube", "name-"+id, embeddingJSON, metadata,
	)
	require.NoError(t, err)
}

func TestRepository_CountAll_ExcludesSoftDeleted(t *testing.T) {
	repo := newInMemoryRepository(t)
	ctx := context.Background()

	seedAsset(t, repo, "kept_1", "[0.1]", "")
	seedAsset(t, repo, "kept_2", "[0.1]", "")
	seedAsset(t, repo, "deleted_1", "[0.1]", "2026-06-01T00:00:00Z")

	n, err := repo.CountAll(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n, "deleted_at sentinel must be excluded")
}

func TestRepository_CountIndexed_OnlyCountsNonEmptyEmbedding(t *testing.T) {
	repo := newInMemoryRepository(t)
	ctx := context.Background()

	seedAsset(t, repo, "indexed_1", "[0.1,0.2]", "")
	seedAsset(t, repo, "indexed_2", "[0.1,0.2,0.3]", "")
	seedAsset(t, repo, "empty_string", "", "")
	seedAsset(t, repo, "empty_json", "[]", "")
	seedAsset(t, repo, "null", "", `{"embedding_json": null}`)

	n, err := repo.CountIndexed(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n, "only non-empty AND non-'[]' embedding_json rows count")
}

func TestRepository_CountIndexed_ExcludesSoftDeleted(t *testing.T) {
	repo := newInMemoryRepository(t)
	ctx := context.Background()

	seedAsset(t, repo, "kept", "[0.1]", "")
	seedAsset(t, repo, "gone", "[0.1]", "2026-06-01T00:00:00Z")

	n, err := repo.CountIndexed(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
}

func TestRepository_ListIndexedIDs_CapRespected(t *testing.T) {
	repo := newInMemoryRepository(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		seedAsset(t, repo, "asset_"+itoa(i), "[0.1]", "")
	}
	seedAsset(t, repo, "no_emb", "", "")
	seedAsset(t, repo, "deleted", "[0.1]", "2026-06-01T00:00:00Z")

	ids, err := repo.ListIndexedIDs(ctx, 3)
	require.NoError(t, err)
	assert.Len(t, ids, 3)

	all, err := repo.ListIndexedIDs(ctx, 100)
	require.NoError(t, err)
	assert.Len(t, all, 4, "limit > indexed count returns all 4 indexed, no_emb excluded, deleted excluded")
}

func TestRepository_ListIndexedIDs_LimitZeroReturnsEmpty(t *testing.T) {
	repo := newInMemoryRepository(t)
	seedAsset(t, repo, "asset_a", "[0.1]", "")

	ids, err := repo.ListIndexedIDs(context.Background(), 0)
	require.NoError(t, err)
	assert.Equal(t, []string{}, ids)

	ids, err = repo.ListIndexedIDs(context.Background(), -1)
	require.NoError(t, err)
	assert.Equal(t, []string{}, ids)
}

func TestRepository_CountMethods_NilDB(t *testing.T) {
	repo := NewRepository(nil, zap.NewNop())
	ctx := context.Background()

	_, err := repo.CountAll(ctx)
	assert.Error(t, err)
	_, err = repo.CountIndexed(ctx)
	assert.Error(t, err)
	_, err = repo.ListIndexedIDs(ctx, 10)
	assert.Error(t, err)
}

// itoa avoids pulling strconv into tests for trivially small integer caps.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [4]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
