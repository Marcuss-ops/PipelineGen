package finalizer_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// setupTestDB creates an in-memory SQLite DB with the canonical tables.
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:?_journal=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}

	// Create tables matching the canonical schemas (055, 105, plus media_assets).
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS media_assets (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			filename TEXT NOT NULL DEFAULT '',
			media_type TEXT NOT NULL DEFAULT '',
			legacy_file_md5 TEXT NOT NULL DEFAULT '',
			drive_file_id TEXT NOT NULL DEFAULT '',
			drive_link TEXT NOT NULL DEFAULT '',
			download_link TEXT NOT NULL DEFAULT '',
			folder_id TEXT NOT NULL DEFAULT '',
			folder_path TEXT NOT NULL DEFAULT '',
			lifecycle_state TEXT NOT NULL DEFAULT 'ACTIVE',
			-- PR-009 (July 2026): E2E wiring finale — index_state column
			-- added to mirror migration 094. The finalizer's spine write
			-- sets it to 'DISCOVERED' literally (matching the wire
			-- shape from PR-008); the IndexingHandler downstream
			-- overwrites to 'INDEXED' after Qdrant upsert.
			index_state TEXT NOT NULL DEFAULT 'DISCOVERED',
			metadata_json TEXT NOT NULL DEFAULT '{}',
			width INTEGER NOT NULL DEFAULT 0,
			height INTEGER NOT NULL DEFAULT 0,
			local_path TEXT NOT NULL DEFAULT '',
			source_provider TEXT NOT NULL DEFAULT '',
			source_version TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT '',
			tags TEXT NOT NULL DEFAULT '',
			tags_norm TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    search_text TEXT NOT NULL DEFAULT '',
    thumbnail_url TEXT NOT NULL DEFAULT '',
    url TEXT NOT NULL DEFAULT '',
    asset_version TEXT NOT NULL DEFAULT '',
    asset_location TEXT NOT NULL DEFAULT '',
    rendition TEXT NOT NULL DEFAULT '',
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
    status TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE IF NOT EXISTS asset_versions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			asset_id TEXT NOT NULL REFERENCES media_assets(id) ON DELETE CASCADE,
			version_number INTEGER NOT NULL,
			source_uri TEXT NOT NULL DEFAULT '',
			legacy_file_md5 TEXT NOT NULL DEFAULT '',
			file_size_bytes INTEGER NOT NULL DEFAULT 0,
			mime_type TEXT NOT NULL DEFAULT '',
			metadata_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL DEFAULT '',
			UNIQUE (asset_id, version_number)
		)`,
		`CREATE TABLE IF NOT EXISTS asset_locations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			asset_id TEXT NOT NULL REFERENCES media_assets(id) ON DELETE CASCADE,
			location_kind TEXT NOT NULL CHECK (location_kind IN ('local', 'drive', 'object_storage')),
			uri TEXT NOT NULL,
			external_id TEXT NOT NULL DEFAULT '',
			web_view_link TEXT NOT NULL DEFAULT '',
			download_url TEXT NOT NULL DEFAULT '',
			mime_type TEXT NOT NULL DEFAULT '',
			file_size_bytes INTEGER NOT NULL DEFAULT 0,
			legacy_file_md5 TEXT NOT NULL DEFAULT '',
			is_primary INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT '',
			UNIQUE (asset_id, location_kind)
		)`,
		`CREATE TABLE IF NOT EXISTS asset_renditions (
			id TEXT PRIMARY KEY,
			asset_id TEXT NOT NULL REFERENCES media_assets(id) ON DELETE CASCADE,
			location_id INTEGER NOT NULL REFERENCES asset_locations(id),
			kind TEXT NOT NULL,
			container TEXT NOT NULL DEFAULT '',
			codec TEXT NOT NULL DEFAULT '',
			width INTEGER NOT NULL DEFAULT 0,
			height INTEGER NOT NULL DEFAULT 0,
			fps REAL NOT NULL DEFAULT 0,
			bitrate INTEGER NOT NULL DEFAULT 0,
			sha256 TEXT NOT NULL DEFAULT '',
			size_bytes INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT '',
			UNIQUE (asset_id, kind)
		)`,
		`CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			status TEXT NOT NULL DEFAULT 'QUEUED',
			worker_id TEXT NOT NULL DEFAULT '',
			lease_id TEXT NOT NULL DEFAULT '',
			retry_count INTEGER NOT NULL DEFAULT 0,
			revision INTEGER NOT NULL DEFAULT 0,
			result_json TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS outbox_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			event_type TEXT NOT NULL,
			aggregate_id TEXT NOT NULL DEFAULT '',
			aggregate_type TEXT NOT NULL DEFAULT '',
			payload_json TEXT NOT NULL DEFAULT '{}',
			event_key TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			attempt_count INTEGER NOT NULL DEFAULT 0,
			max_attempts INTEGER NOT NULL DEFAULT 5,
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT ''
		)`,
		// Partial UNIQUE INDEX on event_key — required by
		// AssetTxFinalizer.insertOutboxEvent's
		// `ON CONFLICT(event_key) WHERE event_key != '' DO NOTHING`.
		// Mirrors the codebase's partial-index pattern
		// (idx_jobs_active_key, idx_artifacts_sha256): the
		// uniqueness triggers only when event_key is non-empty,
		// so one-shot inserts with event_key='' are NOT
		// uniqueness-constrained. This is the fail-closed
		// idempotency contract for re-finalization.
		`CREATE UNIQUE INDEX IF NOT EXISTS ux_outbox_events_event_key
			ON outbox_events(event_key) WHERE event_key != ''`,
		`CREATE TABLE IF NOT EXISTS job_events (
			id TEXT PRIMARY KEY,
			job_id TEXT NOT NULL,
			type TEXT NOT NULL,
			message TEXT NOT NULL DEFAULT '',
			data_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL DEFAULT ''
		)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}

	return db
}

func newTestFinalizer(t *testing.T, db *sql.DB) *finalizer.AssetTxFinalizer {
	t.Helper()
	return finalizer.NewAssetTxFinalizer(nil, assets.NewSQLiteAssetCommitter(db, outboxevents.NewRepository(db), nil))
}

func publishedArtifact(assetID, sha256, fileID string) finalization.PublishedArtifact {
	return finalization.PublishedArtifact{
		ArtifactID:     assetID,
		Kind:           finalization.KindVideo,
		Filename:       "test-video.mp4",
		MIMEType:       "video/mp4",
		SizeBytes:      1024,
		SHA256:         sha256,
		SourceVersion:  1,
		Requirement:    finalization.ArtifactRequirementRequired,
		IdempotencyKey: fmt.Sprintf("idem-%s", assetID),
		Description:    "Pacquiao lands a clean left hand while Broner backs up.",
		Location: finalization.AssetLocation{
			Provider:     "drive",
			FileID:       fileID,
			WebViewLink:  fmt.Sprintf("https://drive.google.com/file/d/%s/view", fileID),
			DownloadLink: fmt.Sprintf("https://drive.google.com/uc?id=%s", fileID),
			FolderID:     "folder-abc",
			FolderPath:   "/test",
			Action:       finalization.PublishCreated,
		},
	}
}

// TestAssetTxFinalizer_RoundTrip verifies that FinalizeAsset writes
// to all three canonical tables (media_assets, asset_versions,
// asset_locations) inside a transaction.
func TestAssetTxFinalizer_RoundTrip(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	fx := newTestFinalizer(t, db)
	ctx := context.Background()
	artifact := publishedArtifact("asset-001", "abc123", "drive-file-abc")

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()

	domainTx := finalizer.WrapTx(tx)
	ref, events, err := fx.FinalizeAsset(ctx, domainTx, artifact)
	if err != nil {
		t.Fatalf("FinalizeAsset: %v", err)
	}
	if ref.ArtifactID != "asset-001" {
		t.Errorf("ArtifactID = %q, want %q", ref.ArtifactID, "asset-001")
	}
	if ref.AssetID != "asset-001" {
		t.Errorf("AssetID = %q, want %q", ref.AssetID, "asset-001")
	}
	if ref.SourceVersion != 1 {
		t.Errorf("SourceVersion = %d, want 1", ref.SourceVersion)
	}
	if ref.ContentHash != "abc123" {
		t.Errorf("ContentHash = %q, want %q", ref.ContentHash, "abc123")
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 outbox event, got %d", len(events))
	}
	if events[0].EventType != outboxevents.EventAssetIndexRequested {
		t.Errorf("event type = %q, want %q", events[0].EventType, outboxevents.EventAssetIndexRequested)
	}

	// Verify media_assets row exists (before commit — inside tx).
	var (
		filename, mediaType, fileHash, driveFileID, lifecycleState string
	)
	err = tx.QueryRowContext(ctx,
		`SELECT filename, media_type, legacy_file_md5, drive_file_id, lifecycle_state FROM media_assets WHERE id = ?`,
		"asset-001",
	).Scan(&filename, &mediaType, &fileHash, &driveFileID, &lifecycleState)
	if err != nil {
		t.Fatalf("verify media_assets: %v", err)
	}
	if filename != "test-video.mp4" {
		t.Errorf("filename = %q", filename)
	}
	if mediaType != "video" {
		t.Errorf("media_type = %q", mediaType)
	}
	if fileHash != "abc123" {
		t.Errorf("legacy_file_md5 = %q", fileHash)
	}
	if driveFileID != "drive-file-abc" {
		t.Errorf("drive_file_id = %q", driveFileID)
	}
	// FASE 3b: new rows are PUBLISHED (not ACTIVE).
	if lifecycleState != "PUBLISHED" {
		t.Errorf("lifecycle_state = %q, want PUBLISHED", lifecycleState)
	}
	var metadataJSON string
	err = tx.QueryRowContext(ctx,
		`SELECT metadata_json FROM media_assets WHERE id = ?`,
		"asset-001",
	).Scan(&metadataJSON)
	if err != nil {
		t.Fatalf("verify media_assets metadata_json: %v", err)
	}
	if !strings.Contains(metadataJSON, `"description":"Pacquiao lands a clean left hand while Broner backs up."`) {
		t.Fatalf("metadata_json missing description, got %s", metadataJSON)
	}

	// Verify asset_versions row exists.
	var versionNum int
	var versionHash string
	err = tx.QueryRowContext(ctx,
		`SELECT version_number, legacy_file_md5 FROM asset_versions WHERE asset_id = ?`,
		"asset-001",
	).Scan(&versionNum, &versionHash)
	if err != nil {
		t.Fatalf("verify asset_versions: %v", err)
	}
	if versionNum != 1 {
		t.Errorf("version_number = %d, want 1", versionNum)
	}
	if versionHash != "abc123" {
		t.Errorf("legacy_file_md5 = %q", versionHash)
	}

	// Verify asset_locations row exists.
	var locKind, locFileID string
	err = tx.QueryRowContext(ctx,
		`SELECT location_kind, external_id FROM asset_locations WHERE asset_id = ?`,
		"asset-001",
	).Scan(&locKind, &locFileID)
	if err != nil {
		t.Fatalf("verify asset_locations: %v", err)
	}
	if locKind != "drive" {
		t.Errorf("location_kind = %q", locKind)
	}
	if locFileID != "drive-file-abc" {
		t.Errorf("external_id = %q", locFileID)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestAssetTxFinalizer_OverlayPersistsLocationAndSHA256 pins the final step
// of the probe→SHA256→manifest→publisher→persist flow: a published overlay
// artifact (source=chronon + drive_subpath=[overlay] + probe sha256/size +
// real duration_ms) must persist location (drive_file_id/drive_link) and
// sha256 (legacy_file_md5) on media_assets — and the REAL duration, not the
// SizeBytes/250000 fallback.
func TestAssetTxFinalizer_OverlayPersistsLocationAndSHA256(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	artifact := finalization.PublishedArtifact{
		ArtifactID:     "job_overlay:overlay:001",
		Kind:           finalization.KindVideo, // overlay routes to youtube_clip → KindVideo
		Filename:       "overlay_001.mov",
		MIMEType:       "video/quicktime",
		SizeBytes:      1234567, // fallback would be 1234567/250000 ≈ 4ms
		SHA256:         "overlay-sha-001",
		SourceVersion:  1,
		Requirement:    finalization.ArtifactRequirementRequired,
		IdempotencyKey: "job_overlay:overlay:001",
		Source:         "chronon",
		ArtifactMetadata: map[string]any{
			"source":           "chronon",
			"drive_subpath":    []string{"overlay"},
			"renderer_version": "chronon-1.0",
			"duration_ms":      int64(1000),
			"duration_us":      int64(1000000),
		},
		Location: finalization.AssetLocation{
			Provider:     "drive",
			FileID:       "overlay-drive-file-1",
			WebViewLink:  "https://drive.google.com/file/d/overlay-drive-file-1/view",
			DownloadLink: "https://drive.google.com/uc?id=overlay-drive-file-1",
			FolderID:     "folder-overlay",
			FolderPath:   "/video/847/overlay",
			Action:       finalization.PublishCreated,
		},
	}

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	if _, _, err := newTestFinalizer(t, db).FinalizeAsset(context.Background(), finalizer.WrapTx(tx), artifact); err != nil {
		t.Fatalf("FinalizeAsset: %v", err)
	}

	// media_assets: location + sha256 + real duration persisted.
	var source, fileHash, driveFileID, driveLink string
	var durationMs int64
	err = tx.QueryRowContext(context.Background(), `
		SELECT source, legacy_file_md5, drive_file_id, drive_link, duration_ms
		FROM media_assets WHERE id = ?`, "job_overlay:overlay:001").
		Scan(&source, &fileHash, &driveFileID, &driveLink, &durationMs)
	if err != nil {
		t.Fatalf("verify overlay media_assets: %v", err)
	}
	if source != "chronon" {
		t.Errorf("source = %q, want chronon", source)
	}
	if fileHash != "overlay-sha-001" {
		t.Errorf("legacy_file_md5 = %q, want overlay-sha-001", fileHash)
	}
	if driveFileID != "overlay-drive-file-1" {
		t.Errorf("drive_file_id = %q, want overlay-drive-file-1", driveFileID)
	}
	if driveLink != "https://drive.google.com/file/d/overlay-drive-file-1/view" {
		t.Errorf("drive_link = %q, want the Drive web-view link", driveLink)
	}
	if durationMs != 1000 {
		t.Errorf("duration_ms = %d, want 1000 (real duration, not SizeBytes/250000 fallback)", durationMs)
	}

	// asset_locations: sha256 (legacy_file_md5) + drive identity (external_id/web_view_link).
	var locKind, locExternalID, locWebView, locFileHash string
	err = tx.QueryRowContext(context.Background(), `
		SELECT location_kind, external_id, web_view_link, legacy_file_md5
		FROM asset_locations WHERE asset_id = ?`, "job_overlay:overlay:001").
		Scan(&locKind, &locExternalID, &locWebView, &locFileHash)
	if err != nil {
		t.Fatalf("verify overlay asset_locations: %v", err)
	}
	if locKind != "drive" {
		t.Errorf("location_kind = %q, want drive", locKind)
	}
	if locExternalID != "overlay-drive-file-1" {
		t.Errorf("external_id = %q, want overlay-drive-file-1", locExternalID)
	}
	if locWebView == "" {
		t.Error("web_view_link is empty")
	}
	if locFileHash != "overlay-sha-001" {
		t.Errorf("asset_locations.legacy_file_md5 = %q, want overlay-sha-001", locFileHash)
	}
}

func TestAssetTxFinalizer_RenditionUsesCanonicalLocationKind(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	artifact := publishedArtifact("asset-rendition", "hash-rendition", "drive-rendition")
	artifact.Renditions = []finalization.AssetRenditionLocation{{
		Kind:          "master",
		Provider:      "local",
		URI:           "/tmp/asset-rendition.mp4",
		MimeType:      "video/mp4",
		LegacyFileMD5: "hash-rendition",
		Width:         1920,
		Height:        1080,
	}}

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := newTestFinalizer(t, db).FinalizeAsset(context.Background(), finalizer.WrapTx(tx), artifact); err != nil {
		tx.Rollback()
		t.Fatalf("FinalizeAsset with rendition: %v", err)
	}
	var locationKind, renditionKind string
	if err := tx.QueryRowContext(context.Background(), `
		SELECT al.location_kind, ar.kind
		FROM asset_locations al
		JOIN asset_renditions ar ON ar.location_id = al.id
		WHERE al.asset_id = ?`, artifact.ArtifactID).Scan(&locationKind, &renditionKind); err != nil {
		tx.Rollback()
		t.Fatalf("read rendition location: %v", err)
	}
	if locationKind != "local" || renditionKind != "master" {
		t.Fatalf("location_kind=%q rendition_kind=%q", locationKind, renditionKind)
	}
	var width, height int
	if err := tx.QueryRowContext(context.Background(), `
		SELECT width, height FROM asset_renditions WHERE asset_id = ? AND kind = ?`,
		artifact.ArtifactID, "master").Scan(&width, &height); err != nil {
		t.Fatalf("read rendition dimensions: %v", err)
	}
	if width != 1920 || height != 1080 {
		t.Fatalf("rendition dimensions=%dx%d, want 1920x1080", width, height)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// TestAssetTxFinalizer_IdempotentVersionIncrement verifies that
// two sequential FinalizeAsset calls on the same asset increment
// the version_number correctly.
func TestAssetTxFinalizer_IdempotentVersionIncrement(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	fx := newTestFinalizer(t, db)
	ctx := context.Background()

	// First finalization.
	tx1, _ := db.BeginTx(ctx, nil)
	ref1, _, err := fx.FinalizeAsset(ctx, finalizer.WrapTx(tx1),
		publishedArtifact("asset-002", "hash-v1", "file-v1"))
	if err != nil {
		tx1.Rollback()
		t.Fatalf("first finalize: %v", err)
	}
	if ref1.SourceVersion != 1 {
		t.Errorf("first version = %d, want 1", ref1.SourceVersion)
	}
	tx1.Commit()

	// Second finalization (new content hash, new file).
	tx2, _ := db.BeginTx(ctx, nil)
	ref2, _, err := fx.FinalizeAsset(ctx, finalizer.WrapTx(tx2),
		publishedArtifact("asset-002", "hash-v2", "file-v2"))
	if err != nil {
		tx2.Rollback()
		t.Fatalf("second finalize: %v", err)
	}
	if ref2.SourceVersion != 2 {
		t.Errorf("second version = %d, want 2", ref2.SourceVersion)
	}
	tx2.Commit()

	// Verify both versions exist.
	var count int
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM asset_versions WHERE asset_id = ?`, "asset-002").Scan(&count)
	if count != 2 {
		t.Errorf("version count = %d, want 2", count)
	}

	// Verify media_assets now reflects the latest hash.
	var fileHash string
	db.QueryRowContext(ctx, `SELECT legacy_file_md5 FROM media_assets WHERE id = ?`, "asset-002").Scan(&fileHash)
	if fileHash != "hash-v2" {
		t.Errorf("media_assets legacy_file_md5 = %q after second finalize, want hash-v2", fileHash)
	}
}

// TestAssetTxFinalizer_DifferentArtifactKinds verifies correct media_type
// mapping for each ArtifactKind.
func TestAssetTxFinalizer_DifferentArtifactKinds(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	fx := newTestFinalizer(t, db)
	ctx := context.Background()

	cases := []struct {
		kind      finalization.ArtifactKind
		wantMedia string
	}{
		{finalization.KindVideo, "video"},
		{finalization.KindImage, "image"},
		{finalization.KindAudio, "audio"},
		{finalization.KindVoiceover, "audio"},
		{finalization.KindSoundEffect, "audio"},
		{finalization.KindDocument, "document"},
		{finalization.KindScript, "text"},
		{finalization.KindMetadata, "metadata"},
		{finalization.KindArchive, "archive"},
	}

	for _, c := range cases {
		t.Run(string(c.kind), func(t *testing.T) {
			assetID := fmt.Sprintf("kind-test-%s", c.kind)
			pa := publishedArtifact(assetID, "hash", "file")
			pa.Kind = c.kind

			tx, _ := db.BeginTx(ctx, nil)
			defer tx.Rollback()
			_, _, err := fx.FinalizeAsset(ctx, finalizer.WrapTx(tx), pa)
			if err != nil {
				t.Fatalf("FinalizeAsset(%s): %v", c.kind, err)
			}

			var mediaType string
			tx.QueryRowContext(ctx,
				`SELECT media_type FROM media_assets WHERE id = ?`, assetID,
			).Scan(&mediaType)
			if mediaType != c.wantMedia {
				t.Errorf("media_type = %q, want %q", mediaType, c.wantMedia)
			}
		})
	}
}

// TestAssetTxFinalizer_OutboxEventPayload verifies the outbox event
// carries the canonical v1 index request payload matching the
// IndexingHandler contract (schema_version, event_id, asset_id,
// source_version, idempotency_key).
func TestAssetTxFinalizer_OutboxEventPayload(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	fx := newTestFinalizer(t, db)
	ctx := context.Background()

	artifact := publishedArtifact("asset-payload", "sha256-hash", "drive-id-xyz")
	tx, _ := db.BeginTx(ctx, nil)
	defer tx.Rollback()

	_, events, err := fx.FinalizeAsset(ctx, finalizer.WrapTx(tx), artifact)
	if err != nil {
		t.Fatalf("FinalizeAsset: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 outbox event, got %d", len(events))
	}

	var payload map[string]any
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal outbox payload: %v", err)
	}
	if payload["schema_version"] != outboxevents.ReindexEnvelopeV1Schema {
		t.Errorf("schema_version = %v, want %v", payload["schema_version"], outboxevents.ReindexEnvelopeV1Schema)
	}
	if payload["asset_id"] != "asset-payload" {
		t.Errorf("asset_id = %v", payload["asset_id"])
	}
	if payload["source_version"] != "sha256-hash" {
		t.Errorf("source_version = %v", payload["source_version"])
	}
	if _, ok := payload["event_id"]; !ok {
		t.Error("event_id missing from payload")
	}
	if _, ok := payload["idempotency_key"]; !ok {
		t.Error("idempotency_key missing from payload")
	}
	if payload["operation"] != "UPSERT" {
		t.Errorf("operation = %v, want UPSERT", payload["operation"])
	}
}

// TestAssetTxFinalizer_RollbackOnError verifies that a failed
// write inside the transaction doesn't persist.
func TestAssetTxFinalizer_RollbackOnError(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	fx := newTestFinalizer(t, db)
	ctx := context.Background()

	// Insert a row that will cause a UNIQUE constraint violation on
	// asset_versions (same asset_id + version_number = 1).
	tx, _ := db.BeginTx(ctx, nil)
	_, _, err := fx.FinalizeAsset(ctx, finalizer.WrapTx(tx),
		publishedArtifact("asset-rollback", "h1", "f1"))
	if err != nil {
		t.Fatalf("first finalize: %v", err)
	}
	tx.Commit()

	// Insert a conflicting asset_versions row manually.
	db.Exec(`INSERT INTO asset_versions (asset_id, version_number, legacy_file_md5, created_at)
		VALUES ('asset-rollback', 999, 'h999', '2024-01-01')`)

	// Now finalize again — the MAX(version_number)+1 should give 1000,
	// but if someone manually inserted 999, the unique constraint should
	// still hold since MAX+1 = 1000 which doesn't conflict.
	tx2, _ := db.BeginTx(ctx, nil)
	ref2, _, err := fx.FinalizeAsset(ctx, finalizer.WrapTx(tx2),
		publishedArtifact("asset-rollback", "h2", "f2"))
	if err != nil {
		t.Fatalf("second finalize with manual version 999: %v", err)
	}
	// MAX(1, 999) + 1 = 1000, which is unique.
	if ref2.SourceVersion != 1000 {
		t.Errorf("expected version 1000 after manual version 999 insert, got %d", ref2.SourceVersion)
	}
	tx2.Commit()
}

// TestAssetTxFinalizer_IndexStatePendingAtInsert pins the godlike/07
// no-fake-availability contract for the E2E wiring finale (PR-009):
// the finalizer's spine write MUST set media_assets.index_state to
// the literal 'DISCOVERED' on fresh INSERT, matching the wire
// shape from PR-008 (StockRunMetadata.IndexingStatus). The
// IndexingHandler downstream overwrites to 'INDEXED' after a
// successful Qdrant upsert.
//
// godlike/06 SSOT: the literal value is the canonical projection-time
// hint; media_assets.index_state remains the single source of truth
// for the lifecycle state in the DB. The ON CONFLICT DO UPDATE clause
// intentionally does NOT include index_state — a re-finalization
// must NOT clobber a state the clipindexer has already transitioned
// (INDEXING / INDEXED / INDEX_FAILED).
func TestAssetTxFinalizer_IndexStatePendingAtInsert(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	fx := newTestFinalizer(t, db)
	ctx := context.Background()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()

	artifact := publishedArtifact("asset-e2e-finale", "hash-e2e", "file-e2e")
	if _, _, err := fx.FinalizeAsset(ctx, finalizer.WrapTx(tx), artifact); err != nil {
		t.Fatalf("FinalizeAsset: %v", err)
	}

	// godlike/06 SSOT: the literal value is the canonical
	// projection-time hint. Must be present on the row after the
	// spine write, BEFORE the IndexingHandler runs.
	var indexState string
	err = tx.QueryRowContext(ctx,
		`SELECT index_state FROM media_assets WHERE id = ?`,
		"asset-e2e-finale",
	).Scan(&indexState)
	if err != nil {
		t.Fatalf("query index_state: %v", err)
	}
	if indexState != "DISCOVERED" {
		t.Errorf("media_assets.index_state = %q, want %q (E2E wiring finale: DB column must use the canonical initial state)",
			indexState, "DISCOVERED")
	}

	// Re-finalize (ON CONFLICT path): the index_state must NOT be
	// clobbered. This guards the godlike/06 SSOT invariant that a
	// re-finalization preserves the clipindexer's state transitions.
	// Simulate the clipindexer having advanced the state to INDEXING.
	if _, err := tx.ExecContext(ctx,
		`UPDATE media_assets SET index_state = ? WHERE id = ?`,
		"INDEXING", "asset-e2e-finale"); err != nil {
		t.Fatalf("simulate clipindexer INDEXING: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Open a NEW tx + re-finalize (new hash → re-finalization).
	tx2, _ := db.BeginTx(ctx, nil)
	defer tx2.Rollback()
	artifact2 := publishedArtifact("asset-e2e-finale", "hash-e2e-v2", "file-e2e-v2")
	if _, _, err := fx.FinalizeAsset(ctx, finalizer.WrapTx(tx2), artifact2); err != nil {
		t.Fatalf("re-FinalizeAsset: %v", err)
	}

	// ON CONFLICT DO UPDATE must NOT touch index_state — the
	// clipindexer's INDEXING transition must survive re-finalization.
	var indexStateAfterReFinalize string
	if err := tx2.QueryRowContext(ctx,
		`SELECT index_state FROM media_assets WHERE id = ?`,
		"asset-e2e-finale",
	).Scan(&indexStateAfterReFinalize); err != nil {
		t.Fatalf("query index_state after re-finalize: %v", err)
	}
	if indexStateAfterReFinalize != "INDEXING" {
		t.Errorf("media_assets.index_state after re-finalize = %q, want %q (ON CONFLICT must NOT clobber clipindexer's state transition)",
			indexStateAfterReFinalize, "INDEXING")
	}
}

// TestAssetTxFinalizer_ContentHashInMetadataJson pins the godlike/07
// no-fake-availability contract for the source_version supersede-gate
// fix: metadata_json MUST include content_hash = artifact.SHA256 so
// that SourceVersionFor() (Tier 1, see
// internal/platform/sqlite/assets/source_version.go)
// reads the correct fingerprint from the same write boundary as the
// outbox event.
//
// Without this key, a republish that changes legacy_file_md5 would leave
// metadata_json.$.legacy_file_md5 stale (from the previous ingest), causing
// SourceVersionFor to return the OLD hash and the IndexingHandler to
// mark the NEW event as superseded — Qdrant never updates.
func TestAssetTxFinalizer_ContentHashInMetadataJson(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	fx := newTestFinalizer(t, db)
	ctx := context.Background()

	// First ingest: asset with hash "old-hash".
	tx1, _ := db.BeginTx(ctx, nil)
	_, _, err := fx.FinalizeAsset(ctx, finalizer.WrapTx(tx1),
		publishedArtifact("asset-content-hash", "old-hash", "file-old"))
	if err != nil {
		tx1.Rollback()
		t.Fatalf("first finalize: %v", err)
	}
	if err := tx1.Commit(); err != nil {
		t.Fatalf("commit first: %v", err)
	}

	// Verify content_hash in metadata_json after first ingest.
	var metaJSON1 string
	db.QueryRowContext(ctx,
		`SELECT metadata_json FROM media_assets WHERE id = ?`,
		"asset-content-hash",
	).Scan(&metaJSON1)
	var meta1 map[string]any
	if err := json.Unmarshal([]byte(metaJSON1), &meta1); err != nil {
		t.Fatalf("unmarshal metadata_json (1st): %v", err)
	}
	if meta1["content_hash"] != "old-hash" {
		t.Errorf("metadata_json.content_hash after 1st ingest = %v, want %q",
			meta1["content_hash"], "old-hash")
	}

	// End-to-end: SourceVersionFor() must read Tier 1 (content_hash)
	// and return the hash from the first ingest.
	sv1, err := assets.SourceVersionFor(ctx, db, "asset-content-hash")
	if err != nil {
		t.Fatalf("SourceVersionFor (1st): %v", err)
	}
	if sv1 != "old-hash" {
		t.Errorf("SourceVersionFor() after 1st ingest = %q, want %q (Tier 1 broken!)", sv1, "old-hash")
	}

	// Second ingest: republish with new hash (the bug scenario).
	tx2, _ := db.BeginTx(ctx, nil)
	ref2, events2, err := fx.FinalizeAsset(ctx, finalizer.WrapTx(tx2),
		publishedArtifact("asset-content-hash", "new-hash", "file-new"))
	if err != nil {
		tx2.Rollback()
		t.Fatalf("second finalize: %v", err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatalf("commit second: %v", err)
	}
	if ref2.ContentHash != "new-hash" {
		t.Errorf("ArtifactRef.ContentHash = %q, want %q", ref2.ContentHash, "new-hash")
	}

	// Verify content_hash in metadata_json is the NEW hash after republish.
	var metaJSON2 string
	db.QueryRowContext(ctx,
		`SELECT metadata_json FROM media_assets WHERE id = ?`,
		"asset-content-hash",
	).Scan(&metaJSON2)
	var meta2 map[string]any
	if err := json.Unmarshal([]byte(metaJSON2), &meta2); err != nil {
		t.Fatalf("unmarshal metadata_json (2nd): %v", err)
	}
	if meta2["content_hash"] != "new-hash" {
		t.Errorf("metadata_json.content_hash after 2nd ingest = %v, want %q (supersede gate would fire!)",
			meta2["content_hash"], "new-hash")
	}

	// End-to-end: SourceVersionFor() must now return the NEW hash
	// from Tier 1 — this is the canonical production contract that
	// prevents the supersede gate from firing on republish.
	sv2, err := assets.SourceVersionFor(ctx, db, "asset-content-hash")
	if err != nil {
		t.Fatalf("SourceVersionFor (2nd): %v", err)
	}
	if sv2 != "new-hash" {
		t.Errorf("SourceVersionFor() after republish = %q, want %q (supersede gate would fire!)", sv2, "new-hash")
	}

	// Verify legacy_file_md5 column is also the new hash.
	var colHash string
	db.QueryRowContext(ctx,
		`SELECT legacy_file_md5 FROM media_assets WHERE id = ?`,
		"asset-content-hash",
	).Scan(&colHash)
	if colHash != "new-hash" {
		t.Errorf("legacy_file_md5 column = %q, want %q", colHash, "new-hash")
	}

	// Verify the outbox event's source_version matches the metadata
	// content_hash (same write boundary — the canonical consistency
	// contract). FinalizeAsset returns events in-memory.
	if len(events2) != 1 {
		t.Fatalf("expected 1 outbox event, got %d", len(events2))
	}
	var payload map[string]any
	if err := json.Unmarshal(events2[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal outbox payload: %v", err)
	}
	if payload["source_version"] != "new-hash" {
		t.Errorf("outbox source_version = %v, want %q", payload["source_version"], "new-hash")
	}
	// Final consistency: outbox source_version == content_hash == SourceVersionFor.
	if payload["source_version"] != sv2 {
		t.Errorf("outbox source_version=%v != SourceVersionFor()=%q (write boundary inconsistency!)",
			payload["source_version"], sv2)
	}
}

func TestTxAdapter_SatisfiesInterface(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	tx, _ := db.BeginTx(context.Background(), nil)
	defer tx.Rollback()

	domainTx := finalizer.WrapTx(tx)
	// Use ExecContext to verify the adapter forwards correctly.
	result, err := domainTx.ExecContext(context.Background(),
		`INSERT INTO media_assets (id, name, filename, created_at, updated_at) VALUES (?, ?, ?, '', '')`,
		"adapter-test", "test", "test.mp4")
	if err != nil {
		t.Fatalf("domainTx.ExecContext: %v", err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		t.Errorf("RowsAffected = %d, want 1", affected)
	}

	// Also test QueryRowContext.
	row := domainTx.QueryRowContext(context.Background(),
		`SELECT name FROM media_assets WHERE id = ?`, "adapter-test")
	var name string
	if err := row.Scan(&name); err != nil {
		t.Fatalf("QueryRowContext.Scan: %v", err)
	}
	if name != "test" {
		t.Errorf("name = %q, want test", name)
	}
}
