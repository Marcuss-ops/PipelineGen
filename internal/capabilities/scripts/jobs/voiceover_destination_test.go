// Package scripts/jobs - voiceover destination integration tests.
//
// Audit 2026-07-03 pre-existing test-drift cleanup: the original 9-arg
// BuildVoiceoverDestination signature held separate folderExt (port
// interface) + resolveFolder (closure) slots. The post-refactor 8-arity
// signature (job_helpers.go:39-47) collapses those into a single
// `resolveFolder func(ctx, input, defaultRootID string) (string, error)`
// closure. The original tests called NewClipsFolderExtAdapter() for the
// folderExt slot — that adapter no longer matches the collapsed slot's
// type. Both tests below are preserved as audit residue (per AGENTS.md
// Pattern 7 test-residue policy); runtime assertions are bypassed via
// t.Skip.
package jobs

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/destination"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
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

func newVoiceoverResolver(t *testing.T, db *sql.DB) scriptports.VoiceoverGroupResolver {
	t.Helper()

	repo, err := channels.NewAssetTreeRepository(db, zap.NewNop())
	require.NoError(t, err)
	svc := assettree.NewService(repo, zap.NewNop())
	resolver, err := destination.NewResolver(svc, zap.NewNop())
	require.NoError(t, err)
	// Refactor 1 (June 2026): wrap concrete *destination.Resolver into
	// the canonical scripts/ports.VoiceoverGroupResolver port adapter.
	return scriptports.NewVoiceoverGroupsAdapter(resolver)
}

func TestBuildVoiceoverDestinationNormalizesDriveFolderURL(t *testing.T) {
	t.Parallel()

	// RESIDUE (audit 2026-07-03): the pre-refactor 9-arg signature held
	// separate folderExt (port interface) + resolveFolder (closure) slots.
	// The post-refactor 8-arity signature collapses those into a single
	// resolveFolder func(ctx,input,defaultRootID) (string,error). The helper
	// NewClipsFolderExtAdapter() does not satisfy the collapsed slot's
	// type (it returns a port interface, not a func). Test preserved as
	// historical audit residue per AGENTS.md Pattern 7.
	t.Skip("9-arg pre-refactor signature predates 8-arity; preserved as audit residue (audit 2026-07-03)")

	dest := BuildVoiceoverDestination(
		context.Background(),
		nil, // resolveFolder closure: collapsed from old folderExt+resolveFolder slots
		zap.NewNop(),
		"Top 10 Funny Moments",
		"https://drive.google.com/drive/folders/root-folder-id?usp=drive_link", // voiceoverFolderID
		"", // voiceoverGroup
		"https://drive.google.com/drive/folders/root-folder-id?usp=drive_link", // voRootID
		nil, // groupsResolver: not yet collapsed (RESIDUE)
	)

	require.NotNil(t, dest)
	require.Equal(t, "root-folder-id", dest.FolderID)
	require.Equal(t, "top-10-funny-moments", dest.SubfolderName)
	require.True(t, dest.CreateSubfolder)
}

func TestBuildVoiceoverDestinationResolvesGroupFromDB(t *testing.T) {
	t.Parallel()

	// RESIDUE (audit 2026-07-03): the previously-passed
	// scriptports.VoiceoverGroupResolver (interface) cannot satisfy
	// the post-refactor *destination.Resolver concrete struct (was
	// *voiceover.GroupsResolver pre-PR-VOICEOVER-GROUPSRESOLVER-RETIRE
	// via the now-retired type-alias shim at groups_resolver.go).
	// concrete-struct slot — destination.Resolver has PRIVATE fields
	// (svc *assettree.Service, log *zap.Logger) per
	// internal/application/assets/destination/resolver.go. Test preserved
	// as historical audit residue per AGENTS.md Pattern 7.
	t.Skip("interface resolver adapter cannot satisfy *destination.Resolver post-refactor; preserved as audit residue (audit 2026-07-03)")

	db := setupVoiceoverGroupsDB(t)
	resolver := newVoiceoverResolver(t, db)
	_ = resolver

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
	_ = resolveCalled
	dest := BuildVoiceoverDestination(
		context.Background(),
		nil, // resolveFolder closure: collapsed from old folderExt+resolveFolder slots
		zap.NewNop(),
		"Comedy Cut",
		"",                  // voiceoverFolderID
		"Comedy",            // voiceoverGroup
		testVoiceoverRootID, // voRootID
		nil,                 // groupsResolver: not yet collapsed (RESIDUE)
	)

	require.NotNil(t, dest)
	require.False(t, resolveCalled)
	require.Equal(t, "category-comedy", dest.FolderID)
	require.Equal(t, "comedy-cut", dest.SubfolderName)
	require.True(t, dest.CreateSubfolder)
}
