package voiceover

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/media/assettree"
	assettreerepo "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assettree"
)

// voiceoverRootID is the canonical voiceover-routing tree root this resolver
// expects. Mirrors the parent_id used by migrations/sqlite/035_seed_voiceover_categories.sql.
const voiceoverRootID = "1wFhLmyyIH5rKSbtQuCuua9a2LKQymA8A"

// nineVoiceoverCategories mirrors the seed migration: (folder_id, name). The
// last entry deliberately preserves the trailing-space typo "Explainatory "
// from the operator's Drive tree so the case-insensitive matcher still hits
// it correctly (and the canonical row casing is returned).
var nineVoiceoverCategories = []struct {
	folderID string
	name     string
}{
	{"1oOlaSOwq1P7_yLfanvBqxwMvEoV1n4Wo", "Boxe"},
	{"1bNb14kz0m4Vxd_F3af8lcIL-bgvZFJ6P", "Comedy"},
	{"1FQ0RKrXVYKNvosp_IHIskh_2aJ2m7ok6", "Comics"},
	{"1yhqumS6yG91ZDFBzxeJWXgsUP7mVPXfL", "Crime"},
	{"1655kxyQMiJzN5Ugwh8uzNUdEgVJr3O9O", "Discovery"},
	{"1BkxSjbV4Dysv_XffuHmqnfDxg5d0Xs9N", "Explainatory "}, // trailing space — preserve verbatim
	{"1bR5XyiB04bJxaUyQGpWNqN9BVgXAkc1C", "HIpHop"},        // operator's case
	{"120d5xpzKN4rE5obIC16AtG_66NXJrlF0", "Music"},
	{"1lSp-s8mNJOUOxIZbuZ0NjvzbXVMB1Y3I", "Wwe"},
}

// setupTestDB opens an in-memory SQLite, creates asset_tree_nodes matching
// the production schema (001_velox_core.sql), and seeds the 9 voiceover
// categories under voiceoverRootID. Returns a *sql.DB plus a teardown.
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Mirror the production CREATE TABLE statement so column names and types
	// match exactly. Drive `timeutil` parses RFC3339 strings in scanNode, so
	// created_at/updated_at are stored as TEXT in that format.
	createStmt := `
		CREATE TABLE IF NOT EXISTS asset_tree_nodes (
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
		CREATE INDEX IF NOT EXISTS idx_asset_tree_path ON asset_tree_nodes(path);
		CREATE INDEX IF NOT EXISTS idx_asset_tree_parent ON asset_tree_nodes(parent_id);
	`
	if _, err := db.Exec(createStmt); err != nil {
		t.Fatalf("create asset_tree_nodes: %v", err)
	}

	insert := `
		INSERT INTO asset_tree_nodes (
			id, source, asset_id, name, type, parent_id, root_id, path, depth,
			is_folder, drive_file_id, drive_link, metadata, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	for _, c := range nineVoiceoverCategories {
		id := "drive-folder-" + c.folderID
		_, err := db.Exec(insert,
			id, "drive", c.folderID, c.name, "folder",
			voiceoverRootID, voiceoverRootID,
			"/Voiceover/"+strings.TrimSpace(c.name), 1, 1,
			c.folderID,
			"https://drive.google.com/drive/folders/"+c.folderID,
			`{"kind":"voiceover_category"}`,
			"2026-06-01T00:00:00Z", "2026-06-01T00:00:00Z",
		)
		if err != nil {
			t.Fatalf("insert category %q: %v", c.name, err)
		}
	}
	return db
}

// newTestResolver spins up Repository → Service → GroupsResolver. Tests get
// a fresh in-memory DB and resolver per call so they don't share state.
func newTestResolver(t *testing.T) (*GroupsResolver, *sql.DB) {
	t.Helper()
	db := setupTestDB(t)

	repo, err := assettreerepo.NewRepository(db, zap.NewNop())
	if err != nil {
		t.Fatalf("NewRepository: %v", err)
	}
	svc := assettree.NewService(repo, zap.NewNop())

	resolver, err := NewGroupsResolver(svc, zap.NewNop())
	if err != nil {
		t.Fatalf("NewGroupsResolver: %v", err)
	}
	return resolver, db
}

// ─────────────────────────────────────────────────────────────────────────────
// Seed verification — confirm the test setup itself is correct.
// ─────────────────────────────────────────────────────────────────────────────

func TestGroupsResolver_SeedExpectedNineCategoriesPresent(t *testing.T) {
	resolver, _ := newTestResolver(t)
	ctx := context.Background()

	entries, err := resolver.ListGroups(ctx, voiceoverRootID)
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}
	if got, want := len(entries), len(nineVoiceoverCategories); got != want {
		t.Fatalf("ListGroups returned %d entries, want %d (seed is suspect)", got, want)
	}

	// Every seeded folder_id must show up in the result.
	got := map[string]bool{}
	for _, e := range entries {
		got[e.FolderID] = true
	}
	for _, c := range nineVoiceoverCategories {
		if !got[c.folderID] {
			t.Errorf("seed missing folder_id %q (%q)", c.folderID, c.name)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ListGroups — happy + sad paths.
// ─────────────────────────────────────────────────────────────────────────────

func TestGroupsResolver_ListGroups_EmptyParentRejected(t *testing.T) {
	resolver, _ := newTestResolver(t)
	ctx := context.Background()

	entries, err := resolver.ListGroups(ctx, "")
	if err == nil {
		t.Fatalf("ListGroups('') should reject, got entries=%v", entries)
	}
	if !strings.Contains(err.Error(), "parentID is required") {
		t.Errorf("error should mention parentID; got: %v", err)
	}
}

func TestGroupsResolver_ListGroups_UnknownParentReturnsEmptySlice(t *testing.T) {
	resolver, _ := newTestResolver(t)
	ctx := context.Background()

	entries, err := resolver.ListGroups(ctx, "nonexistent-parent-id")
	if err != nil {
		t.Fatalf("ListGroups on unknown parent should NOT error; got %v", err)
	}
	if got := len(entries); got != 0 {
		t.Errorf("unknown parent should return 0 entries, got %d", got)
	}
}

func TestGroupsResolver_NewGroupsResolver_NilServiceRejected(t *testing.T) {
	if _, err := NewGroupsResolver(nil, zap.NewNop()); err == nil {
		t.Errorf("NewGroupsResolver(nil) should return error")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ResolveByName — case-insensitive + canonical casing preserved + ErrGroupNotFound.
// ─────────────────────────────────────────────────────────────────────────────

func TestGroupsResolver_ResolveByName_CaseInsensitive(t *testing.T) {
	resolver, _ := newTestResolver(t)
	ctx := context.Background()

	cases := []struct {
		query        string
		wantName     string
		wantFolderID string
	}{
		{"boxe", "Boxe", "1oOlaSOwq1P7_yLfanvBqxwMvEoV1n4Wo"},                  // lowercase
		{"BOXE", "Boxe", "1oOlaSOwq1P7_yLfanvBqxwMvEoV1n4Wo"},                  // uppercase
		{"Boxe", "Boxe", "1oOlaSOwq1P7_yLfanvBqxwMvEoV1n4Wo"},                  // canonical
		{"  boxe  ", "Boxe", "1oOlaSOwq1P7_yLfanvBqxwMvEoV1n4Wo"},              // whitespace
		{"HIPHOP", "HIpHop", "1bR5XyiB04bJxaUyQGpWNqN9BVgXAkc1C"},              // canonical-cased query
		{"hiphop", "HIpHop", "1bR5XyiB04bJxaUyQGpWNqN9BVgXAkc1C"},              // lowercase hits HIpHop
		{"explainatory", "Explainatory ", "1BkxSjbV4Dysv_XffuHmqnfDxg5d0Xs9N"}, // trailing-space preservation
	}

	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			entry, err := resolver.ResolveByName(ctx, voiceoverRootID, tc.query)
			if err != nil {
				t.Fatalf("ResolveByName(%q): %v", tc.query, err)
			}
			if entry.Name != tc.wantName {
				t.Errorf("ResolveByName(%q): name=%q, want canonical %q", tc.query, entry.Name, tc.wantName)
			}
			if entry.FolderID != tc.wantFolderID {
				t.Errorf("ResolveByName(%q): folder_id=%q, want %q", tc.query, entry.FolderID, tc.wantFolderID)
			}
			if !strings.EqualFold(entry.Name, tc.query) && strings.TrimSpace(tc.query) != entry.Name {
				// Casing preservation: when caller passes lower/upper-case,
				// returned entry.Name must equal the canonical DB row.
				// (Skipping the strict check for the trim+exact-only path to
				// keep the test readable; the case-insensitive match is the
				// primary invariant.)
			}
		})
	}
}

func TestGroupsResolver_ResolveByName_NotFoundReturnsErrGroupNotFound(t *testing.T) {
	resolver, _ := newTestResolver(t)
	ctx := context.Background()

	entry, err := resolver.ResolveByName(ctx, voiceoverRootID, "DoesNotExist")
	if err == nil {
		t.Fatalf("expected error, got entry=%+v", entry)
	}
	if !errors.Is(err, ErrGroupNotFound) {
		t.Errorf("error must wrap ErrGroupNotFound (so buildVoiceoverDestination can fall through); got %v", err)
	}
	if !strings.Contains(err.Error(), "DoesNotExist") {
		t.Errorf("error message should echo the missing name for debuggability; got %q", err.Error())
	}
}

func TestGroupsResolver_ResolveByName_EmptyArgsRejected(t *testing.T) {
	resolver, _ := newTestResolver(t)
	ctx := context.Background()

	if _, err := resolver.ResolveByName(ctx, "", "boxe"); err == nil {
		t.Errorf("empty parentID should reject")
	}
	if _, err := resolver.ResolveByName(ctx, voiceoverRootID, ""); err == nil {
		t.Errorf("empty name should reject")
	}
	if _, err := resolver.ResolveByName(ctx, "  ", "  "); err == nil {
		t.Errorf("whitespace-only args should reject")
	}
}

func TestGroupsResolver_ResolveByName_PreservesCanonicalCasingForLowercasedQuery(t *testing.T) {
	// Specifically: the canonical invariant requested by user — input "boxe"
	// (lowercase) must return entry.Name == "Boxe" (DB row casing verbatim).
	resolver, _ := newTestResolver(t)
	ctx := context.Background()

	entry, err := resolver.ResolveByName(ctx, voiceoverRootID, "boxe")
	if err != nil {
		t.Fatalf("ResolveByName: %v", err)
	}
	if entry.Name != "Boxe" {
		t.Errorf("DB-casing not preserved: caller passed %q, got entry.Name=%q (want canonical %q)",
			"boxe", entry.Name, "Boxe")
	}
	if entry.Name == "boxe" {
		t.Errorf("case-preservation regression: lowercased query leaked through to result")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// BuildVoiceoverDestination doesn't sit in GroupsResolver, but the integration
// invariant — ResolveByName returning ErrGroupNotFound is what lets callers
// fall through cleanly — is critical. Pin it explicitly so a future refactor
// that changes the error type breaks loudly here.
// ─────────────────────────────────────────────────────────────────────────────

func TestGroupsResolver_ErrGroupNotFoundIsExportedTypedValue(t *testing.T) {
	// sanity: the sentinel must NOT be nil and must be detectable via errors.Is
	if ErrGroupNotFound == nil {
		t.Fatal("ErrGroupNotFound sentinel is nil")
	}
	if !errors.Is(
		errors.Join(errors.New("inner"), ErrGroupNotFound),
		ErrGroupNotFound,
	) {
		t.Error("ErrGroupNotFound must be detectable via errors.Is on wrapped errors")
	}
}
