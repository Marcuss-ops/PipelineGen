// cmd/admin/internal/drive/drive_migrate_artlist_test.go — TDD coverage
// for the pure seams of the legacy Artlist asset migration.
//
// The command moves the children of the retired Artlist Drive root into
// the canonical stock tree (`<stock root>/artlist`). What is pinned here
// is the DECISION RULE, because that is where an asset can be lost or
// silently duplicated:
//
//  1. a name that already exists at the destination is a conflict, never
//     an overwrite and never a merge;
//  2. a duplicate name inside the retired root is a conflict — only the
//     first occurrence is planned;
//  3. a child without a usable id/name is an error, not a silent skip;
//  4. every other child is planned, and the report is order-stable.
//
// The Drive calls themselves are exercised by the live run; this surface
// keeps the rule honest without a Drive client.

package drive

import (
	"testing"

	"github.com/stretchr/testify/require"

	platformdrive "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

func TestPlanArtlistMigration_PlansEveryCleanChild(t *testing.T) {
	children := []migrationChild{
		{ID: "fld-1", Name: "boxing-search", IsFolder: true},
		{ID: "file-1", Name: "boxing-search/clip-001.mp4"},
	}

	targets := planArtlistMigration(children, map[string]struct{}{})

	require.Len(t, targets, 2)
	require.Equal(t, "planned", targets[0].Status)
	require.Equal(t, "folder", targets[0].Kind)
	require.Equal(t, "planned", targets[1].Status)
	require.Equal(t, "file", targets[1].Kind)
	require.Equal(t, "fld-1", targets[0].ID, "the source listing order MUST be preserved")
}

func TestPlanArtlistMigration_DestinationNameClashIsAConflict(t *testing.T) {
	children := []migrationChild{
		{ID: "fld-existing", Name: "boxing-search", IsFolder: true},
		{ID: "fld-new", Name: "maya", IsFolder: true},
	}

	targets := planArtlistMigration(children, map[string]struct{}{
		"boxing-search": {},
	})

	require.Equal(t, "conflict", targets[0].Status,
		"an existing destination name MUST be a conflict (never overwritten, never merged)")
	require.NotEmpty(t, targets[0].Error)
	require.Equal(t, "planned", targets[1].Status,
		"a non-colliding sibling MUST still be planned")
}

func TestPlanArtlistMigration_DuplicateSourceNameIsAConflict(t *testing.T) {
	children := []migrationChild{
		{ID: "fld-a", Name: "boxing-search", IsFolder: true},
		{ID: "fld-b", Name: "boxing-search", IsFolder: true},
	}

	targets := planArtlistMigration(children, nil)

	require.Equal(t, "planned", targets[0].Status, "the first occurrence is migrated")
	require.Equal(t, "conflict", targets[1].Status,
		"a second child with the same name MUST NOT be planned: moving both would recreate the ambiguity")
}

func TestPlanArtlistMigration_UnusableChildIsAnError(t *testing.T) {
	children := []migrationChild{
		{ID: "", Name: "no-id"},
		{ID: "fld-2", Name: "   "},
	}

	targets := planArtlistMigration(children, nil)

	require.Equal(t, "error", targets[0].Status)
	require.Equal(t, "error", targets[1].Status)
}

func TestPlanArtlistMigration_EmptySource(t *testing.T) {
	targets := planArtlistMigration(nil, nil)
	require.Empty(t, targets, "an empty retired root MUST produce no targets (nothing to migrate)")
}

func TestToMigrationChildren_CarriesFolderKind(t *testing.T) {
	children := toMigrationChildren([]platformdrive.DriveFileInfo{
		{ID: "fld-1", Name: "boxing-search", MimeType: driveFolderMimeType},
		{ID: "file-1", Name: "clip.mp4", MimeType: "video/mp4"},
	})

	require.True(t, children[0].IsFolder, "a folder mime type MUST map to IsFolder")
	require.False(t, children[1].IsFolder, "a media mime type MUST NOT map to IsFolder")
}

func TestCountArtlistMigration_AccumulatesEveryStatus(t *testing.T) {
	report := artlistMigrationReport{
		Results: []artlistMigrationTarget{
			{Status: "moved"},
			{Status: "planned"},
			{Status: "skipped"},
			{Status: "conflict"},
			{Status: "error"},
		},
	}

	countArtlistMigration(&report)

	require.Equal(t, 1, report.Moved)
	require.Equal(t, 1, report.Planned)
	require.Equal(t, 1, report.Skipped)
	require.Equal(t, 1, report.Conflicts)
	require.Equal(t, 1, report.Errors)
}
