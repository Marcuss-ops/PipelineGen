package app

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	assets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
)

// ghostMockQdrant is a minimal implementation of ghostSweepable for
// unit-testing runGhostSweep without spinning up Qdrant.
type ghostMockQdrant struct {
	// pointIDs is the simulated Qdrant collection: every asset_id
	// listed here is treated as an existing Qdrant point.
	pointIDs []string

	// scrollFn is invoked once per page; returning an error
	// simulates a mid-iteration Qdrant failure.
	scrollFn func(batch []string) error

	// deleteCalls records every DeletePoints invocation so the test
	// can assert what would have been sent to Qdrant.
	deleteCalls [][]string
}

func (m *ghostMockQdrant) ScrollAssetIDsPage(ctx context.Context, batchSize int, fn func([]string) error) error {
	if m.scrollFn != nil {
		return m.scrollFn(m.pointIDs)
	}
	if batchSize <= 0 {
		batchSize = 500
	}
	for i := 0; i < len(m.pointIDs); i += batchSize {
		end := i + batchSize
		if end > len(m.pointIDs) {
			end = len(m.pointIDs)
		}
		if err := fn(m.pointIDs[i:end]); err != nil {
			return err
		}
	}
	return nil
}

func (m *ghostMockQdrant) DeletePoints(ctx context.Context, assetIDs []string) error {
	// Defensive copy so the test can inspect what was sent even
	// after runGhostSweep mutates its local slice.
	cp := make([]string, len(assetIDs))
	copy(cp, assetIDs)
	m.deleteCalls = append(m.deleteCalls, cp)
	return nil
}

// openTestSQLite returns a typed *storage.SQLiteDB with the minimum
// schema needed by runGhostSweep already applied. We deliberately do
// NOT run the full migration set — only the media_assets table is
// read. The fixture is itself a typed handle (not raw *sql.DB); the
// canonical wiring for runGhostSweep (which takes
// *assets.ClipsRepository post PG-011) wraps it through the
// newClipsRepo helper below via the embedded .DB accessor on
// *storage.SQLiteDB. Any future caller that needs the raw *sql.DB
// reach it the same way: db.DB.
func openTestSQLite(t *testing.T) *storage.SQLiteDB {
	t.Helper()
	sqliteDB, err := storage.OpenSQLiteDB(":memory:", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqliteDB.Close() })

	_, err = sqliteDB.Exec(`
		CREATE TABLE media_assets (
			id           TEXT PRIMARY KEY,
			source       TEXT,
			name         TEXT,
			tags         TEXT,
			tags_norm    TEXT,
			metadata_json TEXT,
			updated_at   TEXT,
			created_at   NUMERIC
		);
	`)
	require.NoError(t, err)
	return sqliteDB
}

func seedAsset(t *testing.T, sqliteDB *storage.SQLiteDB, id string) {
	t.Helper()
	_, err := sqliteDB.Exec(
		`INSERT INTO media_assets (id, source, name, tags, tags_norm, metadata_json, updated_at, created_at) VALUES (?, 'youtube', ?, '[]', '[]', '{}', '2026-01-01', 0)`,
		id, id,
	)
	require.NoError(t, err)
}

// newClipsRepo wraps the underlying *sql.DB handle (via the embedded
// .DB accessor on *storage.SQLiteDB) into the canonical typed
// *assets.ClipsRepository the production runGhostSweep signature
// expects. Logging is zap.NewNop() — tests don't read it.
//
// Kept local to this test file (not in pkg/testutil) because the
// schema (media_assets) this fixture depends on is not part of the
// canonical migration set the production repos apply.
func newClipsRepo(t *testing.T, db *storage.SQLiteDB) *assets.ClipsRepository {
	t.Helper()
	return assets.NewClipsRepository(db.DB, zap.NewNop())
}

func TestRunGhostSweep_RemovesGhosts(t *testing.T) {
	db := openTestSQLite(t)
	for _, id := range []string{"yt_A", "yt_B", "manual_C"} {
		seedAsset(t, db, id)
	}
	// Qdrant has A, B, C, AND two ghost IDs not in SQLite.
	mock := &ghostMockQdrant{pointIDs: []string{
		"yt_A",
		"yt_B",
		"manual_C",
		"ghost_orphan_1",
		"ghost_orphan_2",
	}}
	clipsRepo := newClipsRepo(t, db)

	deleted, err := runGhostSweep(
		context.Background(),
		mock,
		clipsRepo,
		500,  // scrollBatchSize
		1000, // sqlitePageSize
		zap.NewNop(),
	)
	require.NoError(t, err)
	assert.Equal(t, 2, deleted, "exactly 2 ghost points should be marked deleted")

	// DeletePoints should have been called exactly once with both
	// ghost IDs (runGhostSweep chunks at 100 — both fit in one batch).
	require.Len(t, mock.deleteCalls, 1, "single DeletePoints call expected for ≤100 ghosts")
	sent := append([]string{}, mock.deleteCalls[0]...)
	assert.ElementsMatch(t, []string{"ghost_orphan_1", "ghost_orphan_2"}, sent)
}

func TestRunGhostSweep_NoGhostsIsClean(t *testing.T) {
	db := openTestSQLite(t)
	for _, id := range []string{"clip_x", "clip_y"} {
		seedAsset(t, db, id)
	}
	mock := &ghostMockQdrant{pointIDs: []string{"clip_x", "clip_y"}}
	clipsRepo := newClipsRepo(t, db)

	deleted, err := runGhostSweep(
		context.Background(),
		mock, clipsRepo, 500, 1000, zap.NewNop(),
	)
	require.NoError(t, err)
	assert.Equal(t, 0, deleted)
	assert.Empty(t, mock.deleteCalls, "no DeletePoints call when collection is in sync")
}

func TestRunGhostSweep_AllGhosts(t *testing.T) {
	db := openTestSQLite(t)
	seedAsset(t, db, "real_1")
	mock := &ghostMockQdrant{pointIDs: []string{
		"ghost_a", "ghost_b", "ghost_c", "ghost_d", "ghost_e",
	}}
	clipsRepo := newClipsRepo(t, db)

	deleted, err := runGhostSweep(
		context.Background(),
		mock, clipsRepo, 500, 1000, zap.NewNop(),
	)
	require.NoError(t, err)
	assert.Equal(t, 5, deleted)
	require.Len(t, mock.deleteCalls, 1)
	sent := append([]string{}, mock.deleteCalls[0]...)
	assert.ElementsMatch(t,
		[]string{"ghost_a", "ghost_b", "ghost_c", "ghost_d", "ghost_e"},
		sent)
}

func TestRunGhostSweep_ChunkingAt100(t *testing.T) {
	// 250 ghosts → expect 3 DeletePoints calls (100, 100, 50).
	db := openTestSQLite(t)
	ghosts := make([]string, 250)
	for i := range ghosts {
		ghosts[i] = "ghost_" + string(rune('a'+i%26)) + "_" + itoa(i)
	}
	mock := &ghostMockQdrant{pointIDs: ghosts}
	clipsRepo := newClipsRepo(t, db)

	deleted, err := runGhostSweep(
		context.Background(),
		mock, clipsRepo, 500, 1000, zap.NewNop(),
	)
	require.NoError(t, err)
	assert.Equal(t, 250, deleted)

	// 3 DeletePoints invocations: 100, 100, 50
	require.Len(t, mock.deleteCalls, 3)
	total := 0
	for _, batch := range mock.deleteCalls {
		total += len(batch)
	}
	assert.Equal(t, 250, total)
	assert.Len(t, mock.deleteCalls[0], 100)
	assert.Len(t, mock.deleteCalls[1], 100)
	assert.Len(t, mock.deleteCalls[2], 50)
}

func TestRunGhostSweep_ScrollErrorPropagates(t *testing.T) {
	db := openTestSQLite(t)
	errBoom := errors.New("qdrant 503")
	mock := &ghostMockQdrant{
		scrollFn: func(_ []string) error { return errBoom },
	}
	clipsRepo := newClipsRepo(t, db)

	deleted, err := runGhostSweep(
		context.Background(),
		mock, clipsRepo, 500, 1000, zap.NewNop(),
	)
	require.Error(t, err)
	assert.ErrorIs(t, err, errBoom)
	assert.Equal(t, 0, deleted)
	assert.Empty(t, mock.deleteCalls, "no delete when scroll fails")
}

func TestRunGhostSweep_NilInputs(t *testing.T) {
	db := openTestSQLite(t)
	log := zap.NewNop()
	clipsRepo := newClipsRepo(t, db)
	mock := &ghostMockQdrant{}

	t.Run("nil qdrant", func(t *testing.T) {
		_, err := runGhostSweep(context.Background(), nil, clipsRepo, 500, 1000, log)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "qdrant store is nil")
	})
	t.Run("nil db", func(t *testing.T) {
		_, err := runGhostSweep(context.Background(), mock, nil, 500, 1000, log)
		require.Error(t, err)
		// PG-011 typed-handle migration (June 2026): runGhostSweep
		// third arg is now *assets.ClipsRepository (was *sql.DB),
		// so the nil-check error string changed accordingly.
		assert.Contains(t, err.Error(), "clips repo is nil")
	})
}

// itoa is a stdlib-free alternative for ints → strings to keep this
// helper file dependency-light.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
