package imagesregistry

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

const assetLocationCommitterSchema = `
CREATE TABLE media_assets (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL DEFAULT '',
    media_type TEXT NOT NULL DEFAULT '',
    source_version TEXT NOT NULL DEFAULT '',
    legacy_file_md5 TEXT NOT NULL DEFAULT '',
    drive_file_id TEXT NOT NULL DEFAULT '',
    drive_link TEXT NOT NULL DEFAULT '',
    lifecycle_state TEXT NOT NULL DEFAULT 'ACTIVE',
    updated_at TEXT NOT NULL DEFAULT '',
    filename TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    index_state TEXT NOT NULL DEFAULT '',
    search_text TEXT NOT NULL DEFAULT '',
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
    status TEXT NOT NULL DEFAULT '');
CREATE TABLE asset_locations (
    asset_id TEXT NOT NULL,
    location_kind TEXT NOT NULL,
    uri TEXT NOT NULL DEFAULT '',
    external_id TEXT NOT NULL DEFAULT '',
    web_view_link TEXT NOT NULL DEFAULT '',
    download_url TEXT NOT NULL DEFAULT '',
    mime_type TEXT NOT NULL DEFAULT '',
    file_size_bytes INTEGER NOT NULL DEFAULT 0,
    legacy_file_md5 TEXT NOT NULL DEFAULT '',
    is_primary INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (asset_id, location_kind)
);
CREATE TABLE outbox_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL,
    aggregate_type TEXT NOT NULL,
    payload_json TEXT NOT NULL,
    event_key TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);`

type testRegistryTxWriter struct{}

func (testRegistryTxWriter) RegisterSourceTx(context.Context, *sql.Tx, mediaregistry.AssetSource) error {
	return nil
}
func (testRegistryTxWriter) LinkContentTx(context.Context, *sql.Tx, string, string) error { return nil }
func (testRegistryTxWriter) UpsertTaxonomyTx(context.Context, *sql.Tx, mediaregistry.AssetTaxonomy) error {
	return nil
}
func (testRegistryTxWriter) AppendEventTx(context.Context, *sql.Tx, mediaregistry.Event) (int64, error) {
	return 0, nil
}

func newTestMediaCommitter(db *sql.DB) *SQLiteMediaCommitter {
	box := outboxevents.NewRepository(db)
	return &SQLiteMediaCommitter{
		db: db, box: box, ledger: testRegistryTxWriter{},
		assets: NewSQLiteAssetCommitter(db, box, zap.NewNop()),
	}
}

func openDriveReconciliationDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(assetLocationCommitterSchema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

func insertAssetLocationTestAsset(t *testing.T, db *sql.DB, id, sourceVersion string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO media_assets
		(id, source, media_type, source_version, legacy_file_md5, drive_file_id, drive_link)
		VALUES (?, 'youtube', 'video', ?, ?, 'old-file', 'https://drive.google.com/file/d/old-file/view')`,
		id, sourceVersion, sourceVersion)
	if err != nil {
		t.Fatalf("insert asset %q: %v", id, err)
	}
}

func TestSQLiteMediaCommitter_ReconcileDriveLocations_AtomicUpdateAndIndexEvent(t *testing.T) {
	db := openDriveReconciliationDB(t)
	insertAssetLocationTestAsset(t, db, "asset-1", "hash-1")
	committer := newTestMediaCommitter(db)

	err := committer.ReconcileDriveLocations(context.Background(), []persistence.DriveLocationPatch{{
		AssetID: "asset-1", DriveFileID: "new-file", DriveLink: "https://drive.google.com/file/d/new-file/view",
	}})
	if err != nil {
		t.Fatalf("ReconcileDriveLocations: %v", err)
	}

	var fileID, link string
	if err := db.QueryRow(`SELECT drive_file_id, drive_link FROM media_assets WHERE id = 'asset-1'`).Scan(&fileID, &link); err != nil {
		t.Fatal(err)
	}
	if fileID != "new-file" || link != "https://drive.google.com/file/d/new-file/view" {
		t.Fatalf("media_assets location = (%q, %q)", fileID, link)
	}
	var locationID, locationLink string
	if err := db.QueryRow(`SELECT external_id, web_view_link FROM asset_locations WHERE asset_id = 'asset-1' AND location_kind = 'drive'`).Scan(&locationID, &locationLink); err != nil {
		t.Fatal(err)
	}
	if locationID != "new-file" || locationLink != link {
		t.Fatalf("asset_locations location = (%q, %q)", locationID, locationLink)
	}
	var uri string
	if err := db.QueryRow(`SELECT uri FROM asset_locations WHERE asset_id = 'asset-1' AND location_kind = 'drive'`).Scan(&uri); err != nil {
		t.Fatal(err)
	}
	if uri != "drive://new-file" {
		t.Fatalf("asset_locations uri = %q, want canonical drive:// identifier", uri)
	}
	var eventType, aggregateID string
	if err := db.QueryRow(`SELECT event_type, aggregate_id FROM outbox_events WHERE aggregate_id = 'asset-1'`).Scan(&eventType, &aggregateID); err != nil {
		t.Fatal(err)
	}
	if eventType != outboxevents.EventAssetIndexRequested || aggregateID != "asset-1" {
		t.Fatalf("outbox event = (%q, %q)", eventType, aggregateID)
	}
}

func TestSQLiteMediaCommitter_ReconcileDriveLocations_ClearsLinkPreservesDriveLocation(t *testing.T) {
	db := openDriveReconciliationDB(t)
	insertAssetLocationTestAsset(t, db, "asset-1", "hash-1")
	if _, err := db.Exec(`UPDATE media_assets SET drive_file_id = '' WHERE id = 'asset-1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO asset_locations (asset_id, location_kind, external_id, web_view_link, is_primary) VALUES ('asset-1', 'drive', 'old-file', 'old-link', 1)`); err != nil {
		t.Fatal(err)
	}
	committer := newTestMediaCommitter(db)
	if err := committer.ReconcileDriveLocations(context.Background(), []persistence.DriveLocationPatch{{AssetID: "asset-1"}}); err != nil {
		t.Fatalf("ReconcileDriveLocations: %v", err)
	}
	var fileID, link string
	if err := db.QueryRow(`SELECT drive_file_id, drive_link FROM media_assets WHERE id = 'asset-1'`).Scan(&fileID, &link); err != nil {
		t.Fatal(err)
	}
	if fileID != "old-file" || link != "" {
		t.Fatalf("cleared media_assets location = (%q, %q), want preserved ID and empty link", fileID, link)
	}
	var lifecycle string
	if err := db.QueryRow(`SELECT lifecycle_state FROM media_assets WHERE id = 'asset-1'`).Scan(&lifecycle); err != nil {
		t.Fatal(err)
	}
	if lifecycle != "ERROR" {
		t.Fatalf("cleared unavailable asset lifecycle_state = %q, want ERROR", lifecycle)
	}
	var locationID, locationLink string
	if err := db.QueryRow(`SELECT external_id, web_view_link FROM asset_locations WHERE asset_id = 'asset-1' AND location_kind = 'drive'`).Scan(&locationID, &locationLink); err != nil {
		t.Fatal(err)
	}
	if locationID != "old-file" || locationLink != "" {
		t.Fatalf("preserved drive location = (%q, %q), want old-file and empty link", locationID, locationLink)
	}
}

func TestSQLiteMediaCommitter_ReconcileDriveLocations_RejectsMissingAssetWithoutPartialCommit(t *testing.T) {
	db := openDriveReconciliationDB(t)
	insertAssetLocationTestAsset(t, db, "asset-1", "hash-1")
	committer := newTestMediaCommitter(db)

	err := committer.ReconcileDriveLocations(context.Background(), []persistence.DriveLocationPatch{
		{AssetID: "asset-1", DriveFileID: "new-file", DriveLink: "new-link"},
		{AssetID: "missing-asset", DriveFileID: "missing-file", DriveLink: "missing-link"},
	})
	if err == nil {
		t.Fatal("missing media_assets row must fail closed")
	}
	var fileID string
	if err := db.QueryRow(`SELECT drive_file_id FROM media_assets WHERE id = 'asset-1'`).Scan(&fileID); err != nil {
		t.Fatal(err)
	}
	if fileID != "old-file" {
		t.Fatalf("asset location after missing-row rollback = %q, want old-file", fileID)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM outbox_events`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("outbox rows after missing-row rollback = %d, want 0", count)
	}
}

func TestSQLiteMediaCommitter_ReconcileDriveLocations_RollsBackAssetAndOutboxTogether(t *testing.T) {
	db := openDriveReconciliationDB(t)
	insertAssetLocationTestAsset(t, db, "asset-a", "hash-a")
	insertAssetLocationTestAsset(t, db, "asset-b", "")
	if _, err := db.Exec(`INSERT INTO asset_locations (asset_id, location_kind, external_id, web_view_link, is_primary) VALUES ('asset-a', 'drive', 'old-a', 'old-a-link', 1)`); err != nil {
		t.Fatal(err)
	}
	committer := newTestMediaCommitter(db)

	err := committer.ReconcileDriveLocations(context.Background(), []persistence.DriveLocationPatch{
		{AssetID: "asset-b", DriveFileID: "new-b", DriveLink: "new-b-link"},
		{AssetID: "asset-a", DriveFileID: "new-a", DriveLink: "new-a-link"},
	})
	if err == nil {
		t.Fatal("expected source-version failure")
	}
	var fileID string
	if err := db.QueryRow(`SELECT drive_file_id FROM media_assets WHERE id = 'asset-a'`).Scan(&fileID); err != nil {
		t.Fatal(err)
	}
	if fileID != "old-file" {
		t.Fatalf("asset-a location after rollback = %q, want old-file", fileID)
	}
	var locationID string
	if err := db.QueryRow(`SELECT external_id FROM asset_locations WHERE asset_id = 'asset-a' AND location_kind = 'drive'`).Scan(&locationID); err != nil {
		t.Fatal(err)
	}
	if locationID != "old-a" {
		t.Fatalf("asset-a drive location after rollback = %q, want old-a", locationID)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM outbox_events`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("outbox rows after rollback = %d, want 0", count)
	}
}

func TestSQLiteMediaCommitter_ReconcileDriveLocations_DoesNotTreatLinkOnlyAlternateAsUsable(t *testing.T) {
	db := openDriveReconciliationDB(t)
	insertAssetLocationTestAsset(t, db, "asset-1", "hash-1")
	if _, err := db.Exec(`INSERT INTO asset_locations (asset_id, location_kind, uri, web_view_link, is_primary) VALUES ('asset-1', 'local', '', 'stale-link', 1)`); err != nil {
		t.Fatal(err)
	}
	committer := newTestMediaCommitter(db)
	if err := committer.ReconcileDriveLocations(context.Background(), []persistence.DriveLocationPatch{{AssetID: "asset-1"}}); err != nil {
		t.Fatalf("ReconcileDriveLocations: %v", err)
	}
	var lifecycle string
	if err := db.QueryRow(`SELECT lifecycle_state FROM media_assets WHERE id = 'asset-1'`).Scan(&lifecycle); err != nil {
		t.Fatal(err)
	}
	if lifecycle != "ERROR" {
		t.Fatalf("link-only alternate kept lifecycle_state = %q, want ERROR", lifecycle)
	}
}

func TestSQLiteMediaCommitter_ReconcileDriveLocations_RecognizesVerifiedAlternateLocation(t *testing.T) {
	db := openDriveReconciliationDB(t)
	insertAssetLocationTestAsset(t, db, "asset-1", "hash-1")
	if _, err := db.Exec(`INSERT INTO asset_locations
		(asset_id, location_kind, uri, legacy_file_md5, file_size_bytes, is_primary)
		VALUES ('asset-1', 'local', '/data/asset-1.mp4', 'verified-hash', 1024, 1)`); err != nil {
		t.Fatal(err)
	}
	committer := newTestMediaCommitter(db)
	if err := committer.ReconcileDriveLocations(context.Background(), []persistence.DriveLocationPatch{{AssetID: "asset-1"}}); err != nil {
		t.Fatalf("ReconcileDriveLocations: %v", err)
	}
	var lifecycle string
	if err := db.QueryRow(`SELECT lifecycle_state FROM media_assets WHERE id = 'asset-1'`).Scan(&lifecycle); err != nil {
		t.Fatal(err)
	}
	if lifecycle != "ACTIVE" {
		t.Fatalf("verified alternate location changed lifecycle_state = %q, want ACTIVE", lifecycle)
	}
}

func TestSQLiteMediaCommitter_ReconcileDriveLocations_PreservesLocalPrimary(t *testing.T) {
	db := openDriveReconciliationDB(t)
	insertAssetLocationTestAsset(t, db, "asset-1", "hash-1")
	if _, err := db.Exec(`INSERT INTO asset_locations (asset_id, location_kind, external_id, is_primary) VALUES ('asset-1', 'local', 'local-file', 1)`); err != nil {
		t.Fatal(err)
	}
	committer := newTestMediaCommitter(db)
	if err := committer.ReconcileDriveLocations(context.Background(), []persistence.DriveLocationPatch{{AssetID: "asset-1", DriveFileID: "drive-1", DriveLink: "drive-link"}}); err != nil {
		t.Fatal(err)
	}
	var drivePrimary, localPrimary int
	if err := db.QueryRow(`SELECT is_primary FROM asset_locations WHERE asset_id = 'asset-1' AND location_kind = 'drive'`).Scan(&drivePrimary); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT is_primary FROM asset_locations WHERE asset_id = 'asset-1' AND location_kind = 'local'`).Scan(&localPrimary); err != nil {
		t.Fatal(err)
	}
	if drivePrimary != 0 || localPrimary != 1 {
		t.Fatalf("primary flags drive=%d local=%d, want drive=0 local=1", drivePrimary, localPrimary)
	}
}

func TestSQLiteMediaCommitter_ReconcileDriveLocations_RepeatedLocationIsIdempotent(t *testing.T) {
	db := openDriveReconciliationDB(t)
	insertAssetLocationTestAsset(t, db, "asset-1", "hash-1")
	committer := newTestMediaCommitter(db)
	change := []persistence.DriveLocationPatch{{AssetID: "asset-1", DriveFileID: "new-file", DriveLink: "new-link"}}
	err := committer.ReconcileDriveLocations(context.Background(), change)
	if err != nil {
		t.Fatal(err)
	}
	var firstAssetUpdatedAt, firstLocationUpdatedAt string
	if err := db.QueryRow(`SELECT updated_at FROM media_assets WHERE id = 'asset-1'`).Scan(&firstAssetUpdatedAt); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT updated_at FROM asset_locations WHERE asset_id = 'asset-1' AND location_kind = 'drive'`).Scan(&firstLocationUpdatedAt); err != nil {
		t.Fatal(err)
	}
	if firstAssetUpdatedAt == "" || firstLocationUpdatedAt == "" {
		t.Fatal("first commit must stamp both asset and Drive location timestamps")
	}

	err = committer.ReconcileDriveLocations(context.Background(), change)
	if err != nil {
		t.Fatal(err)
	}
	var secondAssetUpdatedAt, secondLocationUpdatedAt string
	if err := db.QueryRow(`SELECT updated_at FROM media_assets WHERE id = 'asset-1'`).Scan(&secondAssetUpdatedAt); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT updated_at FROM asset_locations WHERE asset_id = 'asset-1' AND location_kind = 'drive'`).Scan(&secondLocationUpdatedAt); err != nil {
		t.Fatal(err)
	}
	if secondAssetUpdatedAt != firstAssetUpdatedAt || secondLocationUpdatedAt != firstLocationUpdatedAt {
		t.Fatalf("idempotent replay changed timestamps: asset %q -> %q, location %q -> %q",
			firstAssetUpdatedAt, secondAssetUpdatedAt, firstLocationUpdatedAt, secondLocationUpdatedAt)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = 'asset-1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("repeated location commits produced %d events, want 1", count)
	}
}

func TestNormalizeDriveLocationPatches_RejectsConflicts(t *testing.T) {
	_, err := normalizeDriveLocationPatches([]persistence.DriveLocationPatch{
		{AssetID: "asset-1", DriveFileID: "a"},
		{AssetID: "asset-1", DriveFileID: "b"},
	})
	if err == nil {
		t.Fatal("expected conflicting changes to fail")
	}
}
