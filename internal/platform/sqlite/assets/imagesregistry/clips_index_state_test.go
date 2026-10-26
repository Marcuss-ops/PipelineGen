// clips_index_state_test.go pins the writer-wave guard of the MEDIA LEGACY
// READ-PLANE DEMOLITION: the operational SQLite plane must REFUSE the terminal
// INDEXED transition, which is owned solely by PostgresIndexWorker on the
// media SSOT (percheck_indexed_state_writer_ssot). The literal-scan gate
// cannot see a dynamic parameter, so the refusal lives at the write boundary
// and is proven here at BOTH seams: the public SetIndexState method and the
// UpdateMediaAssetIndexState primitive it delegates to.
package imagesregistry

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

func newIndexStateTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`
CREATE TABLE media_assets (
    id TEXT PRIMARY KEY,
    index_state TEXT NOT NULL DEFAULT '',
    index_state_updated_at TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    updated_at TEXT NOT NULL DEFAULT ''
);
INSERT INTO media_assets (id, index_state) VALUES ('a1', 'DISCOVERED');
`)
	require.NoError(t, err)
	return db
}

// recordingExecutor records whether the mutation primitive reached SQL at all.
type recordingExecutor struct {
	execs int
}

func (r *recordingExecutor) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	r.execs++
	return nil, errors.New("recordingExecutor: unexpected SQL write")
}

func (r *recordingExecutor) QueryRowContext(context.Context, string, ...any) *sql.Row {
	return nil
}

func TestSetIndexState_RefusesIndexedOwnedByIndexWorker(t *testing.T) {
	for _, state := range []asset.IndexState{
		asset.StateIndexed,
		asset.IndexState("indexed"),
		asset.IndexState(" IndexEd "),
	} {
		db := newIndexStateTestDB(t)
		repo := NewClipsRepository(db, zap.NewNop())

		err := repo.SetIndexState(context.Background(), "a1", state)
		require.Error(t, err, "state %q must be refused", state)
		require.ErrorIs(t, err, ErrIndexStateOwnedByIndexWorker, "state %q", state)

		var stored string
		require.NoError(t, db.QueryRow(`SELECT index_state FROM media_assets WHERE id = 'a1'`).Scan(&stored))
		require.Equal(t, "DISCOVERED", stored, "state %q must not reach the database", state)
	}
}

func TestSetIndexState_StillWritesDeletionChainStates(t *testing.T) {
	db := newIndexStateTestDB(t)
	repo := NewClipsRepository(db, zap.NewNop())

	require.NoError(t, repo.SetIndexState(context.Background(), "a1", asset.StateIndexDeletePending))

	var stored string
	require.NoError(t, db.QueryRow(`SELECT index_state FROM media_assets WHERE id = 'a1'`).Scan(&stored))
	require.Equal(t, string(asset.StateIndexDeletePending), stored)
}

func TestUpdateMediaAssetIndexState_RefusesIndexedBeforeSQL(t *testing.T) {
	exec := &recordingExecutor{}
	err := UpdateMediaAssetIndexState(context.Background(), exec, "a1", "INDEXED", "", "")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrIndexStateOwnedByIndexWorker)
	require.Zero(t, exec.execs, "the refusal must happen before any SQL is executed")
}

func TestUpdateMediaAssetIndexState_AllowsNonIndexedStates(t *testing.T) {
	exec := &recordingExecutor{}
	err := UpdateMediaAssetIndexState(context.Background(), exec, "a1", "DELETE_PENDING", "", "")
	require.Error(t, err) // the recorder always fails the write
	require.NotErrorIs(t, err, ErrIndexStateOwnedByIndexWorker)
	require.Equal(t, 1, exec.execs, "a non-INDEXED state must reach SQL")
	require.False(t, strings.Contains(err.Error(), ErrIndexStateOwnedByIndexWorker.Error()))
}
