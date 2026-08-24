package artifacts

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	drive "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

	"go.uber.org/zap"
)

// mockDriveVerifier is a mock implementation of DriveVerifier
type mockDriveVerifier struct {
	shouldExist bool
	shouldErr   bool
}

func (m *mockDriveVerifier) VerifyDriveLink(ctx context.Context, driveLink string) (bool, error) {
	if m.shouldErr {
		return false, sql.ErrConnDone
	}
	return m.shouldExist, nil
}

// mockRegistry is a mock implementation of Registry
type mockRegistry struct {
	savedRecords map[string]*MediaRecord
	shouldErr    bool
}

func (m *mockRegistry) UpsertMedia(ctx context.Context, rec *MediaRecord) error {
	if m.shouldErr {
		return sql.ErrConnDone
	}
	if m.savedRecords == nil {
		m.savedRecords = make(map[string]*MediaRecord)
	}
	m.savedRecords[rec.ID] = rec
	return nil
}

func (m *mockRegistry) GetMedia(ctx context.Context, id string) (*MediaRecord, error) {
	if m.shouldErr {
		return nil, sql.ErrConnDone
	}
	rec, ok := m.savedRecords[id]
	if !ok {
		return nil, sql.ErrNoRows
	}
	return rec, nil
}

func (m *mockRegistry) DeleteMedia(ctx context.Context, id string) error {
	if m.shouldErr {
		return sql.ErrConnDone
	}
	delete(m.savedRecords, id)
	return nil
}

func (m *mockRegistry) GetAllWithDriveFileID(ctx context.Context) ([]*MediaRecord, error) {
	if m.shouldErr {
		return nil, sql.ErrConnDone
	}
	var result []*MediaRecord
	for _, rec := range m.savedRecords {
		if rec.DriveFileID != "" {
			result = append(result, rec)
		}
	}
	return result, nil
}

func (m *mockRegistry) FindByPHash(ctx context.Context, phash string) (string, error) {
	if m.shouldErr {
		return "", sql.ErrConnDone
	}
	for _, rec := range m.savedRecords {
		if rec.PHash == phash && phash != "" {
			return rec.ID, nil
		}
	}
	return "", sql.ErrNoRows
}

func TestMediaFinalizerVerifiesDriveFile(t *testing.T) {
	ctx := context.Background()
	logger, _ := zap.NewDevelopment()

	// Create a temp file that exists
	tmpFile := filepath.Join(t.TempDir(), "test.mp4")
	if err := os.WriteFile(tmpFile, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}

	// Create mock services
	driveVerifier := &mockDriveVerifier{shouldExist: true}
	registry := &mockRegistry{savedRecords: make(map[string]*MediaRecord)}

	// Create finalizer
	finalizer := NewFinalizer(registry, driveVerifier, logger)

	// Test record with drive link
	rec := &MediaRecord{
		ID:            "test_media_001",
		Name:          "Test Media",
		DriveLink:     "https://drive.google.com/file/d/abc123/view",
		LocalPath:     tmpFile,
		LegacyFileMD5: "hash123",
		Status:        "processed",
	}

	opts := FinalizeOptions{
		RequireLocal: true,
		RequireHash:  true,
		RequireDrive: true,
		VerifyDB:     true,
	}

	result, err := finalizer.Finalize(ctx, rec, opts)
	if err != nil {
		t.Errorf("Finalize failed: %v", err)
	}

	if !result.OK {
		t.Errorf("Expected OK=true, got false. Error: %s", result.Error)
	}

	if !result.DriveUploaded {
		t.Error("Expected DriveUploaded=true")
	}

	if !result.DBSaved {
		t.Error("Expected DBSaved=true")
	}

	t.Log("Drive file verification test passed")
}

// fix/voiceover-require-drive-on-intent: when the caller expressed
// intent to write to Drive (RequireDrive=true) but the upload attempt
// produced no DriveLink (e.g. previous run failed, network blip, quota
// exceeded), the finalizer must FAIL EXPLICITLY — not complete OK
// locally and silently demote Drive from required to optional. Without
// this pin a regression in the voiceover/lifecycle setpoint can let
// the lifecycle chain complete with a local-only record and no error
// surfaced to the API caller.
//
// Verifies the strict-fail branch at finalizer.go line 77: with
// RequireLocal+RequireHash satisfied and a real temp file, the
// RequireDrive check fires first, returns early with
// Error="missing drive link after upload", Status="failed", OK=false,
// and crucially NO DB row is written.
func TestMediaFinalizerFailsWhenDriveFileMissing(t *testing.T) {
	ctx := context.Background()
	logger, _ := zap.NewDevelopment()

	driveVerifier := &mockDriveVerifier{shouldExist: false}
	registry := &mockRegistry{savedRecords: make(map[string]*MediaRecord)}

	finalizer := NewFinalizer(registry, driveVerifier, logger)

	// Real temp file so we don't trip the "local file does not exist"
	// branch (which fires BEFORE the RequireDrive check) — the
	// intent of this test is to reach the RequireDrive branch only.
	tmpFile := filepath.Join(t.TempDir(), "no_drive_link.mp3")
	if err := os.WriteFile(tmpFile, []byte("audio"), 0644); err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}

	rec := &MediaRecord{
		ID:            "test_media_002",
		Name:          "Drive intent set, upload failed",
		DriveLink:     "", // upload attempt failed → no link produced
		LocalPath:     tmpFile,
		LegacyFileMD5: "hash123",
		Status:        "processed",
	}

	opts := FinalizeOptions{
		RequireLocal: true,
		RequireHash:  true,
		RequireDrive: true,  // intent-driven: dest.FolderID was set upstream
		VerifyDB:     false, // strict-fail path is what we're pinning (no DB write before the fail)
	}

	result, err := finalizer.Finalize(ctx, rec, opts)
	if err != nil {
		t.Fatalf("Finalize returned a Go error (finalizer surfaces failures via result, not err): %v", err)
	}

	// Strict-fail: the lifecycle chain must NOT complete OK locally.
	if result.OK {
		t.Errorf("OK must be false when RequireDrive=true and DriveLink=\"\" — regression: drive silently demoted to optional")
	}
	if result.Status != "failed" {
		t.Errorf("Status must be %q, got %q", "failed", result.Status)
	}
	if result.Error != "missing drive link after upload" {
		t.Errorf("Error must be %q, got %q", "missing drive link after upload", result.Error)
	}

	// No DB row should be written — the RequireDrive check returns
	// early BEFORE UpsertMedia, so the registry stays empty.
	if len(registry.savedRecords) != 0 {
		t.Errorf("no DB row should be written on a RequireDrive fail; got %d records in registry.savedRecords", len(registry.savedRecords))
	}
}

func TestMediaFinalizerRequiresLocalPath(t *testing.T) {
	ctx := context.Background()
	logger, _ := zap.NewDevelopment()

	driveVerifier := &mockDriveVerifier{shouldExist: true}
	registry := &mockRegistry{savedRecords: make(map[string]*MediaRecord)}

	finalizer := NewFinalizer(registry, driveVerifier, logger)

	// Test record without local path
	rec := &MediaRecord{
		ID:        "test_media_003",
		Name:      "Test No Local Path",
		DriveLink: "https://drive.google.com/file/d/abc/view",
		// LocalPath is empty
		LegacyFileMD5: "hash123",
	}

	opts := FinalizeOptions{
		RequireLocal: true,
		RequireHash:  true,
		RequireDrive: false,
		VerifyDB:     false,
	}

	result, err := finalizer.Finalize(ctx, rec, opts)
	if err != nil {
		t.Errorf("Finalize failed: %v", err)
	}

	if result.OK {
		t.Error("Expected OK=false when local path is required but missing")
	}

	if result.Error != "missing local path" {
		t.Errorf("Expected 'missing local path' error, got: %s", result.Error)
	}

	t.Log("Missing local path test passed")
}

func TestMediaFinalizerRequiresFileHash(t *testing.T) {
	ctx := context.Background()
	logger, _ := zap.NewDevelopment()

	driveVerifier := &mockDriveVerifier{shouldExist: true}
	registry := &mockRegistry{savedRecords: make(map[string]*MediaRecord)}

	finalizer := NewFinalizer(registry, driveVerifier, logger)

	// Test record without file hash and without local path
	// This ensures the file hash check is reached
	rec := &MediaRecord{
		ID:        "test_media_004",
		Name:      "Test No File Hash",
		DriveLink: "https://drive.google.com/file/d/abc/view",
		// LocalPath is empty, LegacyFileMD5 is empty
	}

	opts := FinalizeOptions{
		RequireLocal: false, // Don't require local path
		RequireHash:  true,
		RequireDrive: false,
		VerifyDB:     false,
	}

	result, err := finalizer.Finalize(ctx, rec, opts)
	if err != nil {
		t.Errorf("Finalize failed: %v", err)
	}

	if result.OK {
		t.Error("Expected OK=false when file hash is required but missing")
	}

	if result.Error != "missing file hash" {
		t.Errorf("Expected 'missing file hash' error, got: %s", result.Error)
	}

	t.Log("Missing file hash test passed")
}

func TestMediaFinalizerLocalFileNotExists(t *testing.T) {
	ctx := context.Background()
	logger, _ := zap.NewDevelopment()

	driveVerifier := &mockDriveVerifier{shouldExist: true}
	registry := &mockRegistry{savedRecords: make(map[string]*MediaRecord)}

	finalizer := NewFinalizer(registry, driveVerifier, logger)

	// Test record with non-existent local file
	rec := &MediaRecord{
		ID:            "test_media_005",
		Name:          "Test Non-existent File",
		DriveLink:     "https://drive.google.com/file/d/abc/view",
		LocalPath:     "/tmp/nonexistent_file_12345.mp4",
		LegacyFileMD5: "hash123",
	}

	opts := FinalizeOptions{
		RequireLocal: true,
		RequireHash:  true,
		RequireDrive: false,
		VerifyDB:     false,
	}

	result, err := finalizer.Finalize(ctx, rec, opts)
	if err != nil {
		t.Errorf("Finalize failed: %v", err)
	}

	if result.OK {
		t.Error("Expected OK=false when local file does not exist")
	}

	if result.Error != "local file does not exist" {
		t.Errorf("Expected 'local file does not exist' error, got: %s", result.Error)
	}

	t.Log("Non-existent local file test passed")
}

func TestMediaFinalizerDBSaveFailure(t *testing.T) {
	ctx := context.Background()
	logger, _ := zap.NewDevelopment()

	driveVerifier := &mockDriveVerifier{shouldExist: true}
	// Registry that returns error
	registry := &mockRegistry{shouldErr: true}

	finalizer := NewFinalizer(registry, driveVerifier, logger)

	rec := &MediaRecord{
		ID:            "test_media_006",
		Name:          "Test DB Failure",
		LocalPath:     "/tmp/test.mp4",
		LegacyFileMD5: "hash123",
	}

	opts := FinalizeOptions{
		RequireLocal: true,
		RequireHash:  true,
		RequireDrive: false,
		VerifyDB:     false,
	}

	result, err := finalizer.Finalize(ctx, rec, opts)
	if err != nil {
		t.Errorf("Finalize failed: %v", err)
	}

	if result.OK {
		t.Error("Expected OK=false when DB save fails")
	}

	t.Logf("DB save failure test: OK=%v, Error=%s", result.OK, result.Error)
}

type testAssetIndexPort struct{ service *assetindex.Service }

func (p testAssetIndexPort) Upsert(ctx context.Context, rec *AssetIndexRecord) error {
	return p.service.Upsert(ctx, &assetindex.AssetRecord{
		AssetID: rec.AssetID, AssetType: rec.AssetType, Source: rec.Source, SourceID: rec.SourceID,
		GroupName: rec.GroupName, Subfolder: rec.Subfolder, LocalPath: rec.LocalPath,
		DriveLink: rec.DriveLink, DownloadLink: rec.DownloadLink, LegacyFileMD5: rec.LegacyFileMD5,
		ContentHash: rec.ContentHash, Status: rec.Status, Metadata: rec.Metadata,
	})
}

func setupTestAssetIndex(t *testing.T) *assetindex.Service {
	t.Helper()

	schema := `
	CREATE TABLE IF NOT EXISTS asset_index (
		asset_id TEXT PRIMARY KEY,
		asset_type TEXT NOT NULL DEFAULT '',
		source TEXT NOT NULL DEFAULT '',
		source_id TEXT NOT NULL DEFAULT '',
		operation_key TEXT NOT NULL DEFAULT '',
		group_name TEXT NOT NULL DEFAULT '',
		subfolder TEXT NOT NULL DEFAULT '',
		local_path TEXT NOT NULL DEFAULT '',
		drive_link TEXT NOT NULL DEFAULT '',
		download_link TEXT NOT NULL DEFAULT '',
		file_hash TEXT NOT NULL DEFAULT '',
		content_hash TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'pending',
		metadata_json TEXT NOT NULL DEFAULT '{}',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		legacy_file_md5 TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX IF NOT EXISTS idx_asset_content_hash ON asset_index(content_hash);
	CREATE INDEX IF NOT EXISTS idx_asset_source ON asset_index(source, source_id);
	CREATE INDEX IF NOT EXISTS idx_asset_status ON asset_index(status);
	`

	db := drive.NewTestDBWithSchema(t, schema)
	repo := assetindex.NewRepository(db)
	return assetindex.NewService(repo)
}

func TestAssetIndexStatusTracksDeliveryState(t *testing.T) {
	for _, tc := range []struct {
		status asset.AssetPublishStatus
		want   string
	}{
		{asset.AssetPublishPending, "pending"},
		{asset.AssetPublishPublishing, "pending"},
		{asset.AssetPublishFailed, "failed"},
		{asset.AssetPublishPublished, "ready"},
		{asset.AssetPublishLocalOnly, "ready"},
	} {
		if got := assetIndexStatus(tc.status); got != tc.want {
			t.Errorf("assetIndexStatus(%q) = %q, want %q", tc.status, got, tc.want)
		}
	}
}

func TestFinalizerWritesToAssetIndex(t *testing.T) {
	ctx := context.Background()
	logger, _ := zap.NewDevelopment()

	driveVerifier := &mockDriveVerifier{shouldExist: true}
	registry := &mockRegistry{savedRecords: make(map[string]*MediaRecord)}
	assetIdx := setupTestAssetIndex(t)

	finalizer := NewFinalizerWithAssetIndex(registry, driveVerifier, testAssetIndexPort{service: assetIdx}, logger)

	// Create a temp file that exists
	tmpFile := filepath.Join(t.TempDir(), "test_asset.mp4")
	if err := os.WriteFile(tmpFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}

	rec := &MediaRecord{
		ID:            "test_asset_001",
		Name:          "Test Asset",
		LocalPath:     tmpFile,
		LegacyFileMD5: "filehash123",
		ContentHash:   "contenthash123",
		Source:        "artlist",
		SourceID:      "clip-123",
		Group:         "comedy",
		Subfolder:     "politics",
		Status:        "processed",
	}

	opts := FinalizeOptions{
		RequireLocal: true,
		RequireHash:  true,
		RequireDrive: false,
		VerifyDB:     true,
	}

	result, err := finalizer.Finalize(ctx, rec, opts)
	if err != nil {
		t.Fatalf("Finalize failed: %v", err)
	}

	if !result.OK {
		t.Errorf("Expected OK=true, got false. Error: %s", result.Error)
	}

	if !result.DBSaved {
		t.Error("Expected DBSaved=true")
	}

	// Verify asset_index was written
	found, err := assetIdx.FindByContentHash(ctx, "contenthash123")
	if err != nil {
		t.Fatalf("failed to find by content hash: %v", err)
	}
	if found == nil {
		t.Fatal("Expected to find record in asset_index")
	}

	assertEqual(t, "test_asset_001", found.AssetID)
	assertEqual(t, "artlist", found.Source)
	assertEqual(t, "contenthash123", found.ContentHash)
	assertEqual(t, "ready", found.Status)
	if found.CreatedAt.IsZero() || found.UpdatedAt.IsZero() {
		t.Error("asset index timestamps must be propagated by the finalizer adapter")
	}
}

func TestFinalizerKeepsOKWhenAssetIndexWriteFails(t *testing.T) {
	ctx := context.Background()
	logger, _ := zap.NewDevelopment()

	driveVerifier := &mockDriveVerifier{shouldExist: true}
	registry := &mockRegistry{savedRecords: make(map[string]*MediaRecord)}
	assetIdx := setupTestAssetIndex(t)

	finalizer := NewFinalizerWithAssetIndex(registry, driveVerifier, testAssetIndexPort{service: assetIdx}, logger)

	// Create a temp file that exists
	tmpFile := filepath.Join(t.TempDir(), "test_asset2.mp4")
	if err := os.WriteFile(tmpFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}

	rec := &MediaRecord{
		ID:            "test_asset_002",
		Name:          "Test Asset 2",
		LocalPath:     tmpFile,
		LegacyFileMD5: "filehash456",
		ContentHash:   "contenthash456",
		Status:        "processed",
	}

	opts := FinalizeOptions{
		RequireLocal: true,
		RequireHash:  true,
		RequireDrive: false,
		VerifyDB:     true,
	}

	result, err := finalizer.Finalize(ctx, rec, opts)
	if err != nil {
		t.Fatalf("Finalize failed: %v", err)
	}

	if !result.OK {
		t.Errorf("Expected OK=true, got false. Error: %s", result.Error)
	}

	if !result.DBSaved {
		t.Error("Expected DBSaved=true (main DB save should succeed)")
	}

	// Verify asset_index was written (since we're using real service, it should work)
	found, err := assetIdx.FindByContentHash(ctx, "contenthash456")
	if err != nil {
		t.Fatalf("failed to find by content hash: %v", err)
	}
	if found == nil {
		t.Fatal("Expected to find record in asset_index")
	}
}

// BLOC5.3 P0.6 no-fake-availability (I2-followup, June 2026): when the
// drive verifier surfaces a transport error, the result.DriveUploaded
// field must NOT silently report the file as accessible. The error is
// surfaced on result.Error so the API caller can distinguish "verify
// failed (transport)" from "verify said file is not in Drive" (both
// previously looked identical via the DriveUploaded field alone).
//
// The overall operation may still complete OK — the verify error is
// informational + visible, not a hard failure. DBSaved stays true
// (the registry write succeeded downstream) and OK stays true; the
// operator sees the verify error in result.Error.
//
// Pins the silent-success removal at finalizer.go (the previous
// `result.DriveUploaded = exists` line that ran regardless of err).
func TestFinalize_DriveVerifyError_SurfaceError(t *testing.T) {
	ctx := context.Background()
	logger, _ := zap.NewDevelopment()

	// mockDriveVerifier with shouldErr=true returns (false, sql.ErrConnDone).
	// Simulates a transport-level failure when contacting the Drive
	// SDK (network blip, auth refresh race, OAuth rate limit, etc.).
	driveVerifier := &mockDriveVerifier{shouldErr: true}
	registry := &mockRegistry{savedRecords: make(map[string]*MediaRecord)}

	finalizer := NewFinalizer(registry, driveVerifier, logger)

	// Real temp file so we don't trip the local-missing branch.
	tmpFile := filepath.Join(t.TempDir(), "verify_err.mp4")
	if err := os.WriteFile(tmpFile, []byte("data"), 0644); err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}

	rec := &MediaRecord{
		ID:            "test_verify_err_001",
		Name:          "Drive link verify transport error",
		LocalPath:     tmpFile,
		LegacyFileMD5: "hash_verify_err",
		DriveLink:     "https://drive.google.com/file/d/abc123/view",
		Status:        "processed",
	}

	opts := FinalizeOptions{
		RequireLocal: true,
		RequireHash:  true,
		RequireDrive: false, // no intent-driven RequireDrive — the verify error is still surfaced
		VerifyDB:     true,
	}

	result, err := finalizer.Finalize(ctx, rec, opts)
	if err != nil {
		t.Fatalf("Finalize returned a Go error (finalizer surfaces failures via result, not err): %v", err)
	}

	// P0.6 no-fake-availability: verify error must NOT silently
	// propagate as DriveUploaded=true. The field is explicit false.
	if result.DriveUploaded {
		t.Errorf("DriveUploaded must be false on verify transport error (silent-success regression), got true. Error: %s", result.Error)
	}

	// The error is surfaced on result.Error so the API caller sees
	// the verify failure (was: log.Warn only — invisible to API caller).
	// sql.ErrConnDone renders as "sql: connection is already closed".
	if !strings.Contains(result.Error, "connection is already closed") {
		t.Errorf("result.Error must contain the verify error message %q (was logged-only before Wave A I2-followup), got: %q", "connection is already closed", result.Error)
	}

	// The overall operation can still complete OK — the verify error
	// is informational, not a hard failure. DBSaved=true because the
	// registry write succeeded downstream.
	if !result.OK {
		t.Errorf("OK must be true (verify error is informational, not a hard failure), got false. Error: %s", result.Error)
	}
	if !result.DBSaved {
		t.Error("DBSaved must be true (verify error is downstream of the registry write)")
	}
}

// TestFinalizer_WriteMetadataJSON_ContentHashInMetadataJson pins the
// supersede-gate fix for the artifacts finalizer: writeMetadataJSON
// MUST include content_hash in the Extra map so that SourceVersionFor()
// reads Tier 1 (highest priority) instead of falling back to stale
// Tier 2 (file_hash from a previous ingest). Without this fix, a
// republish that changes file_hash would leave metadata_json.$.file_hash
// stale and the supersede gate would fire incorrectly.
func TestFinalizer_WriteMetadataJSON_ContentHashInMetadataJson(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "clip.mp4")
	if err := os.WriteFile(tmpFile, []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}

	registry := &mockRegistry{savedRecords: make(map[string]*MediaRecord)}
	logger, _ := zap.NewDevelopment()
	f := NewFinalizer(registry, nil, logger)

	// First finalize: content_hash = "ch-001", file_hash = "fh-001"
	rec := &MediaRecord{
		ID:            "art-001",
		Name:          "Boxing clip",
		LocalPath:     tmpFile,
		LegacyFileMD5: "fh-001",
		ContentHash:   "ch-001",
		Source:        "artlist",
		MediaType:     "video",
		Status:        "processed",
	}

	result, err := f.Finalize(context.Background(), rec, FinalizeOptions{
		RequireLocal: true,
		RequireHash:  true,
		VerifyDB:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK {
		t.Fatalf("finalize not OK: %s", result.Error)
	}

	// Parse the metadata that was persisted on rec.Metadata by writeMetadataJSON
	meta := parseMetadataJSON(t, rec.Metadata)

	// content_hash MUST be present and equal to ContentHash (Tier 1)
	if meta["content_hash"] != "ch-001" {
		t.Errorf("metadata_json.content_hash = %v, want %q (Tier 1 supersede-gate fix)",
			meta["content_hash"], "ch-001")
	}
	if meta["file_hash"] != "fh-001" {
		t.Errorf("metadata_json.file_hash = %v, want %q", meta["file_hash"], "fh-001")
	}

	// Second finalize (republish): new hashes, content_hash MUST update
	rec2 := &MediaRecord{
		ID:            "art-001",
		Name:          "Boxing clip republished",
		LocalPath:     tmpFile,
		LegacyFileMD5: "fh-002",
		ContentHash:   "ch-002",
		Source:        "artlist",
		MediaType:     "video",
		Status:        "processed",
	}

	result2, err := f.Finalize(context.Background(), rec2, FinalizeOptions{
		RequireLocal: true,
		RequireHash:  true,
		VerifyDB:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result2.OK {
		t.Fatalf("republish finalize not OK: %s", result2.Error)
	}

	meta2 := parseMetadataJSON(t, rec2.Metadata)

	if meta2["content_hash"] != "ch-002" {
		t.Errorf("metadata_json.content_hash after republish = %v, want %q (supersede gate would fire!)",
			meta2["content_hash"], "ch-002")
	}
	if meta2["file_hash"] != "fh-002" {
		t.Errorf("metadata_json.file_hash after republish = %v, want %q", meta2["file_hash"], "fh-002")
	}

	// ContentHash fallback: when ContentHash is empty, falls back to LegacyFileMD5
	rec3 := &MediaRecord{
		ID:            "art-002",
		Name:          "No explicit content hash",
		LocalPath:     tmpFile,
		LegacyFileMD5: "fh-fallback",
		Source:        "artlist",
		MediaType:     "video",
		Status:        "processed",
	}

	result3, err := f.Finalize(context.Background(), rec3, FinalizeOptions{
		RequireLocal: true,
		RequireHash:  true,
		VerifyDB:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result3.OK {
		t.Fatalf("fallback finalize not OK: %s", result3.Error)
	}

	meta3 := parseMetadataJSON(t, rec3.Metadata)
	if meta3["content_hash"] != "fh-fallback" {
		t.Errorf("metadata_json.content_hash fallback = %v, want %q (empty ContentHash -> LegacyFileMD5)",
			meta3["content_hash"], "fh-fallback")
	}
}

func parseMetadataJSON(t *testing.T, raw string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("parseMetadataJSON: %v (raw=%q)", err, raw[:min(len(raw), 200)])
	}
	return m
}

func assertEqual(t *testing.T, expected, actual any) {
	t.Helper()
	if expected != actual {
		t.Errorf("expected %v, got %v", expected, actual)
	}
}
