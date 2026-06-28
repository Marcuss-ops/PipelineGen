package jobs

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
)

const testVoiceoverRootID = "voiceover-root-id"

func setupVoiceoverGroupsDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
		CREATE TABLE asset_tree_nodes (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL,
			asset_id TEXT NOT NULL,
			name TEXT NOT NULL,
			type TEXT NOT NULL,
			parent_id TEXT NOT NULL DEFAULT '',
			root_id TEXT NOT NULL DEFAULT '',
			path TEXT NOT NULL DEFAULT '',
			depth INTEGER NOT NULL DEFAULT 0,
			is_folder INTEGER NOT NULL DEFAULT 0,
			drive_file_id TEXT NOT NULL DEFAULT '',
			drive_link TEXT NOT NULL DEFAULT '',
			metadata TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
	`)
	require.NoError(t, err)
	return db
}

func newVoiceoverResolver(t *testing.T, db *sql.DB) *voiceover.GroupsResolver {
	t.Helper()

	repo, err := assets.NewAssetTreeRepository(db, zap.NewNop())
	require.NoError(t, err)
	svc := assettree.NewService(repo, zap.NewNop())
	resolver, err := voiceover.NewGroupsResolver(svc, zap.NewNop())
	require.NoError(t, err)
	return resolver
}

func TestBuildVoiceoverDestinationNormalizesDriveFolderURL(t *testing.T) {
	t.Parallel()

	dest := BuildVoiceoverDestination(
		context.Background(),
		nil,
		zap.NewNop(),
		"Top 10 Funny Moments",
		"https://drive.google.com/drive/folders/root-folder-id?usp=drive_link",
		"",
		"https://drive.google.com/drive/folders/root-folder-id?usp=drive_link",
		nil,
	)

	require.NotNil(t, dest)
	require.Equal(t, "root-folder-id", dest.FolderID)
	require.Equal(t, "top-10-funny-moments", dest.SubfolderName)
	require.True(t, dest.CreateSubfolder)
}

func TestBuildVoiceoverDestinationResolvesGroupFromDB(t *testing.T) {
	t.Parallel()

	db := setupVoiceoverGroupsDB(t)
	resolver := newVoiceoverResolver(t, db)

	_, err := db.Exec(`
		INSERT INTO asset_tree_nodes (
			id, source, asset_id, name, type, parent_id, root_id, path, depth,
			is_folder, drive_file_id, drive_link, metadata, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		"drive-folder-comedy", "drive", "category-comedy", "Comedy", "folder",
		testVoiceoverRootID, testVoiceoverRootID, "/Voiceover/Comedy", 1, 1,
		"category-comedy",
		"https://drive.google.com/drive/folders/category-comedy",
		`{"kind":"voiceover_category"}`,
		"2026-06-01T00:00:00Z",
		"2026-06-01T00:00:00Z",
	)
	require.NoError(t, err)

	resolveCalled := false
	dest := BuildVoiceoverDestination(
		context.Background(),
		func(context.Context, string, string) (string, error) {
			resolveCalled = true
			return "", errors.New("unexpected fallback")
		},
		zap.NewNop(),
		"Comedy Cut",
		"",
		"Comedy",
		testVoiceoverRootID,
		resolver,
	)

	require.NotNil(t, dest)
	require.False(t, resolveCalled)
	require.Equal(t, "category-comedy", dest.FolderID)
	require.Equal(t, "comedy-cut", dest.SubfolderName)
	require.True(t, dest.CreateSubfolder)
}
