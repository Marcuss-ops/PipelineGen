package finalizer

import (
	"context"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	_ "github.com/mattn/go-sqlite3"
	"strings"
	"testing"
)

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

	domainTx := WrapTx(tx)
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

	if _, _, err := newTestFinalizer(t, db).FinalizeAsset(context.Background(), WrapTx(tx), artifact); err != nil {
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
	if _, _, err := newTestFinalizer(t, db).FinalizeAsset(context.Background(), WrapTx(tx), artifact); err != nil {
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
