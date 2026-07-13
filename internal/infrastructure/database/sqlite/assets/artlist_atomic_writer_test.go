// Package assets — artlist_atomic_writer_test.go (PR-ASSET-COMMITTER).
//
// The artlist publish adapter is now a thin mapper over the canonical
// persistence.AssetCommitter. These tests pin:
//
//  1. Validation fails closed BEFORE any DB write.
//  2. Happy path atomically writes media_assets, asset_locations,
//     and one asset.index.requested outbox row.
//  3. Idempotent replay collapses to a single outbox row.
//  4. Nil constructor / nil receiver surfaces at the right boundary.
package assets

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// artlistTestDB creates an in-memory SQLite database with the minimal
// schema subset required by AssetCommitter.
func artlistTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "artlist_atomic_test.db") + "?cache=shared&_busy_timeout=5000"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS media_assets (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    filename TEXT NOT NULL DEFAULT '',
    media_type TEXT NOT NULL DEFAULT '',
    file_hash TEXT NOT NULL DEFAULT '',
    drive_file_id TEXT NOT NULL DEFAULT '',
    drive_link TEXT NOT NULL DEFAULT '',
    download_link TEXT NOT NULL DEFAULT '',
    local_path TEXT NOT NULL DEFAULT '',
    folder_id TEXT NOT NULL DEFAULT '',
    folder_path TEXT NOT NULL DEFAULT '',
    lifecycle_state TEXT NOT NULL DEFAULT '',
    index_state TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    search_text TEXT NOT NULL DEFAULT '',
    source_version TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT '',
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
    title TEXT NOT NULL DEFAULT ''
);`); err != nil {
		t.Fatalf("CREATE TABLE media_assets: %v", err)
	}

	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS asset_locations (
    asset_id TEXT NOT NULL,
    location_kind TEXT NOT NULL DEFAULT '',
    uri TEXT NOT NULL DEFAULT '',
    external_id TEXT NOT NULL DEFAULT '',
    web_view_link TEXT NOT NULL DEFAULT '',
    download_url TEXT NOT NULL DEFAULT '',
    mime_type TEXT NOT NULL DEFAULT '',
    file_size_bytes INTEGER NOT NULL DEFAULT 0,
    file_hash TEXT NOT NULL DEFAULT '',
    is_primary INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (asset_id, location_kind)
);`); err != nil {
		t.Fatalf("CREATE TABLE asset_locations: %v", err)
	}

	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS outbox_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type TEXT NOT NULL DEFAULT '',
    aggregate_id TEXT NOT NULL DEFAULT '',
    aggregate_type TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL DEFAULT '{}',
    event_key TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_outbox_events_event_key
    ON outbox_events(event_key);`); err != nil {
		t.Fatalf("CREATE TABLE outbox_events: %v", err)
	}

	return db
}

func newArtlistTestAdapter(t *testing.T, db *sql.DB) *artlistPublishTxAdapter {
	t.Helper()
	fixed := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	box := outboxevents.NewRepository(db)
	a := newArtlistPublishTxAdapter(db, box, zap.NewNop())
	a.now = func() time.Time { return fixed }
	return a
}

func validArtlistCommand(assetID string) ArtlistPublishCommand {
	return ArtlistPublishCommand{
		AssetID:       assetID,
		AssetVersion:  "v1",
		AssetLocation: "/data/artlist/staging/" + assetID + ".mp4",
		Rendition:     "1080p",
		DriveFileID:   "drive-file-" + assetID,
		DriveLink:     "https://drive.google.com/file/d/drive-file-" + assetID + "/view",
		DownloadLink:  "https://drive.google.com/uc?export=download&id=drive-file-" + assetID,
		FileHash:      "sha256:" + assetID + "-hash",
		SourceVersion: "sha256:" + assetID + "-hash",
	}
}

func countOutboxRowsForAsset(t *testing.T, db *sql.DB, assetID string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = ?`, assetID).Scan(&n); err != nil {
		t.Fatalf("count outbox rows for asset %q: %v", assetID, err)
	}
	return n
}

type mediaAssetRow struct {
	Source         string
	AssetVersion   string
	AssetLocation  string
	Rendition      string
	DriveFileID    string
	DriveLink      string
	DownloadLink   string
	FileHash       string
	SourceVersion  string
	LifecycleState string
	CreatedAt      string
	UpdatedAt      string
}

func getMediaAssetRow(t *testing.T, db *sql.DB, assetID string) mediaAssetRow {
	t.Helper()
	var r mediaAssetRow
	err := db.QueryRow(`
SELECT source, asset_version, asset_location, rendition,
       drive_file_id, drive_link, download_link,
       file_hash, source_version, lifecycle_state,
       created_at, updated_at
FROM media_assets WHERE id = ?`, assetID).Scan(
		&r.Source, &r.AssetVersion, &r.AssetLocation, &r.Rendition,
		&r.DriveFileID, &r.DriveLink, &r.DownloadLink,
		&r.FileHash, &r.SourceVersion, &r.LifecycleState,
		&r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		t.Fatalf("get media_assets row %q: %v", assetID, err)
	}
	return r
}

func TestCommitArtlistPublishTx_HappyPath_BothRowsPersist(t *testing.T) {
	db := artlistTestDB(t)
	a := newArtlistTestAdapter(t, db)
	cmd := validArtlistCommand("ast-001")

	if err := a.CommitArtlistPublishTx(context.Background(), cmd); err != nil {
		t.Fatalf("CommitArtlistPublishTx: unexpected error: %v", err)
	}

	row := getMediaAssetRow(t, db, "ast-001")
	if row.LifecycleState != "PUBLISHED" {
		t.Errorf("lifecycle_state = %q, want PUBLISHED", row.LifecycleState)
	}
	if row.AssetVersion != cmd.AssetVersion {
		t.Errorf("asset_version = %q, want %q", row.AssetVersion, cmd.AssetVersion)
	}
	if row.AssetLocation != cmd.AssetLocation {
		t.Errorf("asset_location = %q, want %q", row.AssetLocation, cmd.AssetLocation)
	}
	if row.Rendition != cmd.Rendition {
		t.Errorf("rendition = %q, want %q", row.Rendition, cmd.Rendition)
	}
	if row.DriveFileID != cmd.DriveFileID {
		t.Errorf("drive_file_id = %q, want %q", row.DriveFileID, cmd.DriveFileID)
	}
	if row.DriveLink != cmd.DriveLink {
		t.Errorf("drive_link = %q, want %q", row.DriveLink, cmd.DriveLink)
	}
	if row.DownloadLink != cmd.DownloadLink {
		t.Errorf("download_link = %q, want %q", row.DownloadLink, cmd.DownloadLink)
	}
	if row.FileHash != cmd.FileHash {
		t.Errorf("file_hash = %q, want %q", row.FileHash, cmd.FileHash)
	}
	if row.SourceVersion != cmd.SourceVersion {
		t.Errorf("source_version = %q, want %q", row.SourceVersion, cmd.SourceVersion)
	}
	if row.Source != "artlist" {
		t.Errorf("source = %q, want artlist", row.Source)
	}

	var locCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM asset_locations WHERE asset_id = ?`, "ast-001").Scan(&locCount); err != nil {
		t.Fatalf("count asset_locations: %v", err)
	}
	if locCount != 1 {
		t.Errorf("asset_locations rows = %d, want 1", locCount)
	}

	if got := countOutboxRowsForAsset(t, db, "ast-001"); got != 1 {
		t.Errorf("outbox rows for ast-001 = %d, want 1", got)
	}
	var eventType, eventKey, payloadStr string
	if err := db.QueryRow(`SELECT event_type, event_key, payload_json FROM outbox_events WHERE aggregate_id = ?`, "ast-001").Scan(
		&eventType, &eventKey, &payloadStr,
	); err != nil {
		t.Fatalf("query outbox row: %v", err)
	}
	if eventType != outboxevents.EventAssetIndexRequested {
		t.Errorf("event_type = %q, want %q", eventType, outboxevents.EventAssetIndexRequested)
	}
	wantPrefix := outboxevents.EventAssetIndexRequested + ":artlist:ast-001:"
	if !strings.HasPrefix(eventKey, wantPrefix) {
		t.Errorf("event_key = %q, want prefix %q", eventKey, wantPrefix)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(payloadStr), &payload); err != nil {
		t.Fatalf("unmarshal payload_json: %v", err)
	}
	if payload["source"] != "artlist" {
		t.Errorf("payload.source = %v, want artlist", payload["source"])
	}
	if payload["media_type"] != "video" {
		t.Errorf("payload.media_type = %v, want video", payload["media_type"])
	}
	if payload["asset_id"] != "ast-001" {
		t.Errorf("payload.asset_id = %v, want ast-001", payload["asset_id"])
	}
	if payload["idempotency_key"] != eventKey {
		t.Errorf("payload.idempotency_key = %v, want %q", payload["idempotency_key"], eventKey)
	}
}

func TestCommitArtlistPublishTx_Validation_EmptyFieldsFailClosed(t *testing.T) {
	db := artlistTestDB(t)
	a := newArtlistTestAdapter(t, db)
	base := validArtlistCommand("ast-validate")

	cases := []struct {
		name   string
		mutate func(*ArtlistPublishCommand)
		want   error
	}{
		{"empty AssetID", func(c *ArtlistPublishCommand) { c.AssetID = "" }, errArtlistEmptyAssetID},
		{"empty AssetVersion", func(c *ArtlistPublishCommand) { c.AssetVersion = "" }, errArtlistEmptyAssetVersion},
		{"empty AssetLocation", func(c *ArtlistPublishCommand) { c.AssetLocation = "" }, errArtlistEmptyAssetLocation},
		{"empty Rendition", func(c *ArtlistPublishCommand) { c.Rendition = "" }, errArtlistEmptyRendition},
		{"empty DriveFileID", func(c *ArtlistPublishCommand) { c.DriveFileID = "" }, errArtlistEmptyDriveFileID},
		{"empty DriveLink", func(c *ArtlistPublishCommand) { c.DriveLink = "" }, errArtlistEmptyDriveLink},
		{"empty DownloadLink", func(c *ArtlistPublishCommand) { c.DownloadLink = "" }, errArtlistEmptyDownloadLink},
		{"empty FileHash", func(c *ArtlistPublishCommand) { c.FileHash = "" }, errArtlistEmptyFileHash},
		{"empty SourceVersion", func(c *ArtlistPublishCommand) { c.SourceVersion = "" }, errArtlistEmptySourceVersion},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cmd := base
			tc.mutate(&cmd)
			err := a.CommitArtlistPublishTx(context.Background(), cmd)
			if err == nil {
				t.Fatalf("expected validation error, got nil")
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("expected %v in chain, got: %v", tc.want, err)
			}
			if got := countOutboxRowsForAsset(t, db, cmd.AssetID); got != 0 {
				t.Errorf("outbox rows for %q = %d, want 0 (validation runs pre-tx)", cmd.AssetID, got)
			}
		})
	}
}

func TestCommitArtlistPublishTx_IdempotencyDedup(t *testing.T) {
	db := artlistTestDB(t)
	a := newArtlistTestAdapter(t, db)
	cmd := validArtlistCommand("ast-003")

	if err := a.CommitArtlistPublishTx(context.Background(), cmd); err != nil {
		t.Fatalf("first CommitArtlistPublishTx: %v", err)
	}
	if got := countOutboxRowsForAsset(t, db, "ast-003"); got != 1 {
		t.Fatalf("outbox rows after first commit = %d, want 1", got)
	}

	if err := a.CommitArtlistPublishTx(context.Background(), cmd); err != nil {
		t.Fatalf("second CommitArtlistPublishTx: %v", err)
	}
	if got := countOutboxRowsForAsset(t, db, "ast-003"); got != 1 {
		t.Errorf("outbox rows after second commit = %d, want 1 (idempotent)", got)
	}

	row := getMediaAssetRow(t, db, "ast-003")
	if row.LifecycleState != "PUBLISHED" {
		t.Errorf("lifecycle_state = %q, want PUBLISHED", row.LifecycleState)
	}
}

func TestNewArtlistPublishTxAdapter_PanicsOnNil(t *testing.T) {
	box := outboxevents.NewRepository(artlistTestDB(t))

	t.Run("nil db panics", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Errorf("expected panic on nil db, got none")
			}
		}()
		_ = NewArtlistPublishTxAdapter(nil, box, zap.NewNop())
	})
	t.Run("nil box panics", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Errorf("expected panic on nil box, got none")
			}
		}()
		_ = NewArtlistPublishTxAdapter(artlistTestDB(t), nil, zap.NewNop())
	})
}

func TestCommitArtlistPublishTx_AdapterNilSafety(t *testing.T) {
	var a *artlistPublishTxAdapter // nil
	cmd := validArtlistCommand("ast-nil")
	err := a.CommitArtlistPublishTx(context.Background(), cmd)
	if err == nil {
		t.Fatal("expected error from nil adapter, got nil")
	}
	if !strings.Contains(err.Error(), "not wired") {
		t.Errorf("expected 'not wired' in error, got: %v", err)
	}
}
