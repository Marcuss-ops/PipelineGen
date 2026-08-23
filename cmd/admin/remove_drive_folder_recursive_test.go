package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// recursiveMediaAssetsFixture creates the minimal media_assets table
// the PLAN-phase listing helpers touch (id, name, source, folder_id,
// parent_folder_id, lifecycle_state, metadata_json).
func recursiveMediaAssetsFixture(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite3: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`
		CREATE TABLE media_assets (
			id               TEXT PRIMARY KEY,
			name             TEXT NOT NULL DEFAULT '',
			source           TEXT NOT NULL DEFAULT '',
			folder_id        TEXT NOT NULL DEFAULT '',
			parent_folder_id TEXT NOT NULL DEFAULT '',
			lifecycle_state  TEXT NOT NULL DEFAULT 'ACTIVE',
			metadata_json    TEXT NOT NULL DEFAULT '{}',
    filename TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    index_state TEXT NOT NULL DEFAULT '',
    search_text TEXT NOT NULL DEFAULT '',
    source_version TEXT NOT NULL DEFAULT '',
    thumbnail_url TEXT NOT NULL DEFAULT '',
    url TEXT NOT NULL DEFAULT '',
    asset_version TEXT NOT NULL DEFAULT '',
    asset_location TEXT NOT NULL DEFAULT '',
    rendition TEXT NOT NULL DEFAULT '',
    source_provider TEXT NOT NULL DEFAULT '',
    source_video_id TEXT NOT NULL DEFAULT '',
    source_url TEXT NOT NULL DEFAULT '',
    start_ms INTEGER NOT NULL DEFAULT 0,
    end_ms INTEGER NOT NULL DEFAULT 0,
    title TEXT NOT NULL DEFAULT '',
    origin TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT '',
    namespace TEXT NOT NULL DEFAULT '',
    asset_kind TEXT NOT NULL DEFAULT '',
    source_type TEXT NOT NULL DEFAULT '',
    semantic_role TEXT NOT NULL DEFAULT '',
    drive_folder_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '')
	`)
	if err != nil {
		t.Fatalf("create media_assets fixture: %v", err)
	}
	return db
}

// TestCollectUniqueAssets_DeduplicatesByAssetID pins the canonical-asset-id
// dedup: an asset discoverable via BOTH folder_id and parent_folder_id (and
// via --source-video-ids) must be counted exactly once, never twice.
func TestCollectUniqueAssets_DeduplicatesByAssetID(t *testing.T) {
	db := recursiveMediaAssetsFixture(t)
	ctx := context.Background()

	// asset-1 is discoverable under folder-A (folder_id) AND folder-B
	// (parent_folder_id) — it must deduplicate to a single candidate.
	// asset-2 is only under folder-A.
	// asset-3 is under folder-C AND matches the source-video-id selection.
	seeds := []string{
		`INSERT INTO media_assets (id, name, source, folder_id, parent_folder_id) VALUES ('asset-1', 'clip a', 'artlist', 'folder-A', 'folder-B')`,
		`INSERT INTO media_assets (id, name, source, folder_id) VALUES ('asset-2', 'clip b', 'stock', 'folder-A')`,
		`INSERT INTO media_assets (id, name, source, folder_id, metadata_json) VALUES ('asset-3', 'clip c', 'youtube', 'folder-C', '{"source_video_id":"vid-1"}')`,
	}
	for _, q := range seeds {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	folders := []folderInfo{
		{ID: "folder-A", Name: "A"},
		{ID: "folder-B", Name: "B"},
		{ID: "folder-C", Name: "C"},
	}

	got, rawRefs, err := collectUniqueAssets(ctx, db, folders, "vid-1")
	if err != nil {
		t.Fatalf("collectUniqueAssets: %v", err)
	}

	want := map[string]bool{"asset-1": true, "asset-2": true, "asset-3": true}
	if len(got) != len(want) {
		t.Fatalf("unique assets: want %d (asset-1, asset-2, asset-3), got %d: %v", len(want), len(got), got)
	}
	for _, a := range got {
		if !want[a.ID] {
			t.Errorf("unexpected asset %q in plan; want %v", a.ID, want)
		}
	}

	// Deterministic order (sorted by asset ID).
	for i := 1; i < len(got); i++ {
		if got[i-1].ID >= got[i].ID {
			t.Errorf("assets must be sorted by ID; got %q before %q", got[i-1].ID, got[i].ID)
		}
	}

	// Raw references: asset-1 twice (folder-A + folder-B), asset-2 once,
	// asset-3 twice (folder-C + source-video) = 5 references, 3 unique.
	if rawRefs != 5 {
		t.Errorf("raw references: want 5 got %d", rawRefs)
	}
	if rawRefs-len(got) != 2 {
		t.Errorf("duplicates: want 2 got %d", rawRefs-len(got))
	}
}

// TestCollectUniqueAssets_SourceVideoIDOnly pins the --assets-only /
// --source-video-ids selection path when no folders are scanned.
func TestCollectUniqueAssets_SourceVideoIDOnly(t *testing.T) {
	db := recursiveMediaAssetsFixture(t)
	ctx := context.Background()

	if _, err := db.Exec(
		`INSERT INTO media_assets (id, name, source, metadata_json) VALUES ('asset-v', 'clip v', 'youtube', '{"source_video_id":"vid-9"}')`,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, rawRefs, err := collectUniqueAssets(ctx, db, nil, "vid-9")
	if err != nil {
		t.Fatalf("collectUniqueAssets: %v", err)
	}
	if len(got) != 1 || got[0].ID != "asset-v" {
		t.Fatalf("want exactly [asset-v]; got %v", got)
	}
	if rawRefs != 1 {
		t.Errorf("raw references: want 1 got %d", rawRefs)
	}
}

// fakeDeletionHandler is a minimal outboxevents.Handler for preflight tests.
type fakeDeletionHandler struct {
	eventType string
}

func (f fakeDeletionHandler) EventType() string                                { return f.eventType }
func (f fakeDeletionHandler) IdempotencyKey() string                           { return f.eventType + ".v1" }
func (f fakeDeletionHandler) Handle(context.Context, outboxevents.Event) error { return nil }

// TestCheckDeletionHandlers_MissingHandler pins the fail-fast contract: a
// registry without the Drive/Qdrant delete handlers must abort BEFORE the
// first mutation.
func TestCheckDeletionHandlers_MissingHandler(t *testing.T) {
	err := checkDeletionHandlers(outboxevents.NewHandlerRegistry())
	if err == nil {
		t.Fatal("expected error for a registry with no handlers; got nil")
	}
	if !strings.Contains(err.Error(), "asset.drive.delete_requested") {
		t.Errorf("error must name the missing drive handler; got: %q", err.Error())
	}
}

// TestCheckDeletionHandlers_AllHandlersPresent pins the happy path: both
// canonical deletion handlers registered → nil.
func TestCheckDeletionHandlers_AllHandlersPresent(t *testing.T) {
	registry := outboxevents.NewHandlerRegistry()
	for _, evt := range deletionPreflightEventTypes {
		if err := registry.Register(fakeDeletionHandler{eventType: evt}); err != nil {
			t.Fatalf("register %s: %v", evt, err)
		}
	}
	if err := checkDeletionHandlers(registry); err != nil {
		t.Fatalf("checkDeletionHandlers: %v", err)
	}
}

// TestCheckDeletionHandlers_NilRegistry pins the nil-registry guard.
func TestCheckDeletionHandlers_NilRegistry(t *testing.T) {
	if err := checkDeletionHandlers(nil); err == nil {
		t.Fatal("expected error for nil registry; got nil")
	}
}

// TestCheckMediaAssetsReady_TableMissing pins the SQLite readiness gate.
func TestCheckMediaAssetsReady_TableMissing(t *testing.T) {
	db := recursiveMediaAssetsFixture(t)
	if _, err := db.Exec(`DROP TABLE media_assets`); err != nil {
		t.Fatalf("drop media_assets: %v", err)
	}
	if err := checkMediaAssetsReady(db); err == nil {
		t.Fatal("expected error when media_assets is missing; got nil")
	}
}

// TestCheckMediaAssetsReady_TablePresent pins the happy path.
func TestCheckMediaAssetsReady_TablePresent(t *testing.T) {
	db := recursiveMediaAssetsFixture(t)
	if err := checkMediaAssetsReady(db); err != nil {
		t.Fatalf("checkMediaAssetsReady: %v", err)
	}
}

// TestWriteDeletionPlan_PersistsManifest pins the pre-mutation manifest:
// the plan (root, folders, raw refs, unique assets, duplicates) round-trips
// through JSON so recovery can reconstruct the exact scheduled set.
func TestWriteDeletionPlan_PersistsManifest(t *testing.T) {
	plan := deletionPlan{
		RootFolderID: "root-1",
		DeleteRoot:   true,
		Folders:      363,
		AssetsRaw:    239,
		AssetsUnique: 234,
		Duplicates:   5,
		FolderIDs:    []string{"f1", "f2"},
		AssetIDs:     []string{"a1", "a2"},
	}

	path := filepath.Join(t.TempDir(), "plan.json")
	if err := writeDeletionPlan(plan, path); err != nil {
		t.Fatalf("writeDeletionPlan: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read plan: %v", err)
	}
	var got deletionPlan
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal plan: %v", err)
	}
	if got.RootFolderID != plan.RootFolderID || got.DeleteRoot != plan.DeleteRoot ||
		got.Folders != plan.Folders || got.AssetsRaw != plan.AssetsRaw ||
		got.AssetsUnique != plan.AssetsUnique || got.Duplicates != plan.Duplicates {
		t.Errorf("plan round-trip mismatch: got %+v", got)
	}
	if got.AssetsRaw-got.AssetsUnique != got.Duplicates {
		t.Errorf("duplicates must equal raw - unique; got %d - %d = %d (want %d)",
			got.AssetsRaw, got.AssetsUnique, got.AssetsRaw-got.AssetsUnique, got.Duplicates)
	}
}
