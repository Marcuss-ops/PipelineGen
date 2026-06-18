package assetlocations

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// testDB creates an in-memory SQLite database with the asset_locations
// and media_assets tables needed for FK constraints. The media_assets
// table is minimal — only the columns required by the FK.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	// Enable FK support (off by default in go-sqlite3).
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable FKs: %v", err)
	}
	// Minimal media_assets for FK references.
	schema := `
	CREATE TABLE media_assets (id TEXT PRIMARY KEY, source TEXT, name TEXT);
	` + assetLocationsSchema()
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	return db
}

// assetLocationsSchema returns the CREATE TABLE statement matching
// migrations/sqlite/{055,056}_asset_locations*.sql so tests stay in
// sync. The schema includes migration 056's Drive-specific columns
// (external_id, web_view_link, download_url, is_public).
func assetLocationsSchema() string {
	return `
	CREATE TABLE asset_locations (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		asset_id        TEXT NOT NULL REFERENCES media_assets(id) ON DELETE CASCADE,
		location_kind   TEXT NOT NULL CHECK (location_kind IN ('local', 'drive', 'object_storage')),
		uri             TEXT NOT NULL,
		external_id     TEXT NOT NULL DEFAULT '',
		web_view_link   TEXT NOT NULL DEFAULT '',
		download_url    TEXT NOT NULL DEFAULT '',
		is_public       INTEGER NOT NULL DEFAULT 0,
		mime_type       TEXT NOT NULL DEFAULT '',
		file_size_bytes INTEGER NOT NULL DEFAULT 0,
		file_hash       TEXT NOT NULL DEFAULT '',
		is_primary      INTEGER NOT NULL DEFAULT 0,
		created_at      TEXT NOT NULL DEFAULT '',
		updated_at      TEXT NOT NULL DEFAULT '',
		UNIQUE (asset_id, location_kind)
	);
	CREATE INDEX IF NOT EXISTS idx_asset_locations_asset ON asset_locations (asset_id);
	CREATE INDEX IF NOT EXISTS idx_asset_locations_primary ON asset_locations (asset_id) WHERE is_primary = 1;
	CREATE INDEX IF NOT EXISTS idx_asset_locations_drive_external ON asset_locations (external_id) WHERE location_kind = 'drive';
	`
}

// seedAsset inserts a minimal row into media_assets so FK constraints pass.
func seedAsset(t *testing.T, db *sql.DB, id, source string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO media_assets(id, source, name) VALUES (?,?,?)`, id, source, id+"_name"); err != nil {
		t.Fatalf("seed asset %s: %v", id, err)
	}
}

func TestUpsertAndGetByAssetID(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	seedAsset(t, db, "asset-1", "youtube")

	repo := NewRepository(db)

	// Insert local location.
	err := repo.Upsert(ctx, AssetLocation{
		AssetID:       "asset-1",
		LocationKind:  LocationLocal,
		URI:           "/data/clips/asset-1.mp4",
		MimeType:      "video/mp4",
		FileSizeBytes: 1024000,
		FileHash:      "abc123",
		IsPrimary:     true,
	})
	if err != nil {
		t.Fatalf("Upsert local: %v", err)
	}

	// Insert Drive location.
	err = repo.Upsert(ctx, AssetLocation{
		AssetID:      "asset-1",
		LocationKind: LocationDrive,
		URI:          "drive_file_xyz",
		IsPrimary:    false,
	})
	if err != nil {
		t.Fatalf("Upsert drive: %v", err)
	}

	// Retrieve all locations.
	locs, err := repo.GetByAssetID(ctx, "asset-1")
	if err != nil {
		t.Fatalf("GetByAssetID: %v", err)
	}
	if len(locs) != 2 {
		t.Fatalf("expected 2 locations, got %d", len(locs))
	}

	// Primary comes first (ORDER BY is_primary DESC).
	if locs[0].LocationKind != LocationLocal || !locs[0].IsPrimary {
		t.Errorf("local location should be primary, got kind=%s primary=%v", locs[0].LocationKind, locs[0].IsPrimary)
	}
	if locs[0].URI != "/data/clips/asset-1.mp4" {
		t.Errorf("local URI = %q, want /data/clips/asset-1.mp4", locs[0].URI)
	}
	if locs[0].FileSizeBytes != 1024000 {
		t.Errorf("FileSizeBytes = %d, want 1024000", locs[0].FileSizeBytes)
	}
	if locs[0].FileHash != "abc123" {
		t.Errorf("FileHash = %q, want abc123", locs[0].FileHash)
	}
}

func TestGetPrimary(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	seedAsset(t, db, "asset-2", "artlist")

	repo := NewRepository(db)

	// No primary set yet → nil.
	loc, err := repo.GetPrimary(ctx, "asset-2")
	if err != nil {
		t.Fatalf("GetPrimary empty: %v", err)
	}
	if loc != nil {
		t.Fatalf("expected nil primary for new asset, got %+v", loc)
	}

	// Insert with primary.
	if err := repo.Upsert(ctx, AssetLocation{
		AssetID: "asset-2", LocationKind: LocationLocal, URI: "/tmp/a2.mp4", IsPrimary: true,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	loc, err = repo.GetPrimary(ctx, "asset-2")
	if err != nil {
		t.Fatalf("GetPrimary: %v", err)
	}
	if loc == nil || loc.LocationKind != LocationLocal {
		t.Fatalf("expected local primary, got %+v", loc)
	}
}

func TestSetPrimarySwapsDesignation(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	seedAsset(t, db, "asset-3", "stock")

	repo := NewRepository(db)

	// Upsert local (primary) and drive (not primary).
	_ = repo.Upsert(ctx, AssetLocation{AssetID: "asset-3", LocationKind: LocationLocal, URI: "/tmp/a3.mp4", IsPrimary: true})
	_ = repo.Upsert(ctx, AssetLocation{AssetID: "asset-3", LocationKind: LocationDrive, URI: "drive_aaa", IsPrimary: false})

	// Swap primary to drive.
	if err := repo.SetPrimary(ctx, "asset-3", LocationDrive); err != nil {
		t.Fatalf("SetPrimary drive: %v", err)
	}

	loc, err := repo.GetPrimary(ctx, "asset-3")
	if err != nil {
		t.Fatalf("GetPrimary after swap: %v", err)
	}
	if loc == nil || loc.LocationKind != LocationDrive {
		t.Fatalf("expected drive primary after swap, got %+v", loc)
	}

	// Old primary must no longer be primary.
	locs, _ := repo.GetByAssetID(ctx, "asset-3")
	for _, l := range locs {
		if l.LocationKind == LocationLocal && l.IsPrimary {
			t.Errorf("local should no longer be primary after swap")
		}
	}
}

func TestUpsertIdempotent(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	seedAsset(t, db, "asset-4", "youtube")

	repo := NewRepository(db)

	// First upsert.
	_ = repo.Upsert(ctx, AssetLocation{AssetID: "asset-4", LocationKind: LocationLocal, URI: "/tmp/v1.mp4", FileHash: "hash1"})
	// Second upsert with different URI — should update, not duplicate.
	_ = repo.Upsert(ctx, AssetLocation{AssetID: "asset-4", LocationKind: LocationLocal, URI: "/tmp/v2.mp4", FileHash: "hash2"})

	locs, err := repo.GetByAssetID(ctx, "asset-4")
	if err != nil {
		t.Fatalf("GetByAssetID: %v", err)
	}
	if len(locs) != 1 {
		t.Fatalf("expected 1 location after idempotent upsert, got %d", len(locs))
	}
	if locs[0].URI != "/tmp/v2.mp4" {
		t.Errorf("URI = %q, want /tmp/v2.mp4 (upsert should update)", locs[0].URI)
	}
	if locs[0].FileHash != "hash2" {
		t.Errorf("FileHash = %q, want hash2", locs[0].FileHash)
	}
}

func TestDelete(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	seedAsset(t, db, "asset-5", "youtube")

	repo := NewRepository(db)

	_ = repo.Upsert(ctx, AssetLocation{AssetID: "asset-5", LocationKind: LocationLocal, URI: "/tmp/del.mp4"})
	_ = repo.Upsert(ctx, AssetLocation{AssetID: "asset-5", LocationKind: LocationDrive, URI: "drive_del"})

	if err := repo.Delete(ctx, "asset-5", LocationLocal); err != nil {
		t.Fatalf("Delete local: %v", err)
	}

	locs, _ := repo.GetByAssetID(ctx, "asset-5")
	if len(locs) != 1 {
		t.Fatalf("expected 1 location after delete, got %d", len(locs))
	}
	if locs[0].LocationKind != LocationDrive {
		t.Errorf("expected drive to remain, got %s", locs[0].LocationKind)
	}
}

func TestDeleteAll(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	seedAsset(t, db, "asset-6", "youtube")

	repo := NewRepository(db)
	_ = repo.Upsert(ctx, AssetLocation{AssetID: "asset-6", LocationKind: LocationLocal, URI: "/tmp/a.mp4"})
	_ = repo.Upsert(ctx, AssetLocation{AssetID: "asset-6", LocationKind: LocationDrive, URI: "drive_a"})

	if err := repo.DeleteAll(ctx, "asset-6"); err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}
	locs, _ := repo.GetByAssetID(ctx, "asset-6")
	if len(locs) != 0 {
		t.Fatalf("expected 0 locations after DeleteAll, got %d", len(locs))
	}
}

// TestUpsertDrivePopulatesDriveColumns verifies migration 056's four
// new columns (external_id, web_view_link, download_url, is_public)
// are written and re-read round-trip. Also exercises GetDriveLocation.
func TestUpsertDrivePopulatesDriveColumns(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	seedAsset(t, db, "asset-drive", "artlist")

	repo := NewRepository(db)

	if err := repo.Upsert(ctx, AssetLocation{
		AssetID:      "asset-drive",
		LocationKind: LocationDrive,
		URI:          "drive_file_abc",
		ExternalID:   "drive_file_abc",
		WebViewLink:  "https://drive.google.com/file/d/drive_file_abc/view",
		DownloadURL:  "https://www.googleapis.com/drive/v3/files/drive_file_abc?alt=media",
		IsPublic:     true,
		IsPrimary:    true,
	}); err != nil {
		t.Fatalf("Upsert drive: %v", err)
	}

	loc, err := repo.GetDriveLocation(ctx, "asset-drive")
	if err != nil {
		t.Fatalf("GetDriveLocation: %v", err)
	}
	if loc == nil {
		t.Fatal("expected non-nil Drive location")
	}
	if loc.ExternalID != "drive_file_abc" {
		t.Errorf("ExternalID = %q, want drive_file_abc", loc.ExternalID)
	}
	if loc.WebViewLink != "https://drive.google.com/file/d/drive_file_abc/view" {
		t.Errorf("WebViewLink = %q", loc.WebViewLink)
	}
	if loc.DownloadURL != "https://www.googleapis.com/drive/v3/files/drive_file_abc?alt=media" {
		t.Errorf("DownloadURL = %q", loc.DownloadURL)
	}
	if !loc.IsPublic {
		t.Errorf("IsPublic = false, want true")
	}

	// Cross-reference lookup should find the same row via ExternalID.
	byExt, err := repo.GetByExternalID(ctx, "drive_file_abc")
	if err != nil {
		t.Fatalf("GetByExternalID: %v", err)
	}
	if byExt == nil || byExt.AssetID != "asset-drive" {
		t.Fatalf("GetByExternalID missed lookup: %+v", byExt)
	}

	// Empty ExternalID is a quiet no-op so callers don't need to guard.
	empty, err := repo.GetByExternalID(ctx, "")
	if err != nil {
		t.Fatalf("GetByExternalID empty: %v", err)
	}
	if empty != nil {
		t.Errorf("empty external_id should return nil, got %+v", empty)
	}

	// Re-upsert with different Drive metadata should update, not duplicate.
	if err := repo.Upsert(ctx, AssetLocation{
		AssetID:      "asset-drive",
		LocationKind: LocationDrive,
		URI:          "drive_file_v2",
		ExternalID:   "drive_file_v2",
		WebViewLink:  "https://drive.google.com/file/d/drive_file_v2/view",
		DownloadURL:  "https://www.googleapis.com/drive/v3/files/drive_file_v2?alt=media",
		IsPublic:     false,
		IsPrimary:    true,
	}); err != nil {
		t.Fatalf("Upsert drive re: %v", err)
	}
	locs, _ := repo.GetByAssetID(ctx, "asset-drive")
	if len(locs) != 1 {
		t.Fatalf("expected 1 row after idempotent re-upsert, got %d", len(locs))
	}
	if locs[0].ExternalID != "drive_file_v2" {
		t.Errorf("ExternalID after re-upsert = %q, want drive_file_v2", locs[0].ExternalID)
	}
	if locs[0].IsPublic {
		t.Errorf("IsPublic after re-upsert = true, want false")
	}
}

func TestForeignKeyCascade(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	seedAsset(t, db, "asset-fk", "youtube")

	repo := NewRepository(db)
	_ = repo.Upsert(ctx, AssetLocation{AssetID: "asset-fk", LocationKind: LocationLocal, URI: "/tmp/fk.mp4"})

	// Deleting the parent asset should cascade-delete its locations.
	if _, err := db.Exec(`DELETE FROM media_assets WHERE id = ?`, "asset-fk"); err != nil {
		t.Fatalf("delete parent asset: %v", err)
	}

	locs, _ := repo.GetByAssetID(ctx, "asset-fk")
	if len(locs) != 0 {
		t.Fatalf("expected 0 locations after FK cascade, got %d", len(locs))
	}
}
