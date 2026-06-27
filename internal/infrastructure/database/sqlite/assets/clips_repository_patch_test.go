// QDRANT-005 (TODO 6, June 2026) patch test suite for *ClipsRepository.
//
// Spec coverage:
//   1. UpdateFileHash patches ONLY file_hash; lifecycle_state is untouched.
//   2. UpdateDriveLocation is idempotent on same id (no duplicates).
//   3. UpdateProcessingMetadata patches ONLY metadata_json; file_hash
//      and lifecycle_state are untouched.
//   4. 0 RowsAffected returns a typed "asset not found" error rather
//      than silently succeeding (positive control for the patch contract).
package assets

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	drive "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"go.uber.org/zap"
)

// testSchemaForQDRANT005 mirrors the canonical media_assets schema
// (drive.CanonicalMediaAssetsSchema, single source of truth for the
// production column set). New canonical columns added by future
// migrations automatically appear here via the constant embed.
const testSchemaForQDRANT005 = drive.CanonicalMediaAssetsSchema

// newTestRepoForQDRANT005 builds a fresh ClipsRepository backed by
// an in-memory SQLite DB with the canonical schema. The DB is
// auto-closed via t.Cleanup.
func newTestRepoForQDRANT005(t *testing.T) (*ClipsRepository, *sql.DB) {
	t.Helper()
	db := drive.NewTestDBWithSchema(t, testSchemaForQDRANT005)
	t.Cleanup(func() { _ = db.Close() })
	repo := NewClipsRepository(db, zap.NewNop())
	return repo, db
}

// seedAssetForQDRANT005 inserts a minimal media_assets row with the
// given lifecycle_state + file_hash so each patch test exercises a
// specific baseline. metadata_json starts as the empty object so
// UpdateProcessingMetadata tests build on a known-good state.
func seedAssetForQDRANT005(t *testing.T, db *sql.DB, id, lifecycleState, fileHash string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.ExecContext(
		context.Background(),
		`INSERT INTO media_assets (id, source, name, lifecycle_state, file_hash, metadata_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, "test", id, lifecycleState, fileHash, "{}", now, now,
	)
	if err != nil {
		t.Fatalf("seed insert %s: %v", id, err)
	}
}

// ── Spec case 1: UpdateFileHash does NOT touch lifecycle_state ──────

// TestQDRANT005_UpdateFileHash_DoesNotTouchLifecycleState asserts that
// calling UpdateFileHash leaves the row's lifecycle_state unchanged.
func TestQDRANT005_UpdateFileHash_DoesNotTouchLifecycleState(t *testing.T) {
	repo, db := newTestRepoForQDRANT005(t)
	seedAssetForQDRANT005(t, db, "asset-1", "ACTIVE", "")

	if err := repo.UpdateFileHash(context.Background(), "asset-1", "new-hash-123"); err != nil {
		t.Fatalf("UpdateFileHash: %v", err)
	}

	var lifecycleState, fileHash string
	if err := db.QueryRow(
		`SELECT lifecycle_state, file_hash FROM media_assets WHERE id = 'asset-1'`,
	).Scan(&lifecycleState, &fileHash); err != nil {
		t.Fatalf("select: %v", err)
	}
	if lifecycleState != "ACTIVE" {
		t.Errorf("lifecycle_state = %q, want ACTIVE (UpdateFileHash must NOT touch lifecycle_state)", lifecycleState)
	}
	if fileHash != "new-hash-123" {
		t.Errorf("file_hash = %q, want new-hash-123", fileHash)
	}
}

// ── Spec case 2: UpdateDriveLocation is idempotent on same id ──────

// TestQDRANT005_UpdateDriveLocation_Idempotent asserts that two
// consecutive calls with the same arguments leave a single row with
// the same drive_file_id + drive_link values, and that lifecycle_state
// is unchanged.
func TestQDRANT005_UpdateDriveLocation_Idempotent(t *testing.T) {
	repo, db := newTestRepoForQDRANT005(t)
	seedAssetForQDRANT005(t, db, "asset-2", "ACTIVE", "preexisting-h")

	const (
		driveID  = "drive-file-xyz"
		driveURL = "https://drive.google.com/file/d/drive-file-xyz/view"
	)

	if err := repo.UpdateDriveLocation(context.Background(), "asset-2", driveID, driveURL); err != nil {
		t.Fatalf("first UpdateDriveLocation: %v", err)
	}
	if err := repo.UpdateDriveLocation(context.Background(), "asset-2", driveID, driveURL); err != nil {
		t.Fatalf("second UpdateDriveLocation: %v", err)
	}

	var rowCount int
	var recordedDriveID, recordedDriveURL, lifecycleState string
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM media_assets WHERE id = 'asset-2'`,
	).Scan(&rowCount); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rowCount != 1 {
		t.Errorf("row count = %d, want 1 (idempotent: no duplicate rows)", rowCount)
	}
	if err := db.QueryRow(
		`SELECT drive_file_id, drive_link, lifecycle_state FROM media_assets WHERE id = 'asset-2'`,
	).Scan(&recordedDriveID, &recordedDriveURL, &lifecycleState); err != nil {
		t.Fatalf("select: %v", err)
	}
	if recordedDriveID != driveID {
		t.Errorf("drive_file_id = %q, want %q", recordedDriveID, driveID)
	}
	if recordedDriveURL != driveURL {
		t.Errorf("drive_link = %q, want %q", recordedDriveURL, driveURL)
	}
	if lifecycleState != "ACTIVE" {
		t.Errorf("lifecycle_state = %q, want ACTIVE", lifecycleState)
	}
}

// ── Spec case 3: UpdateProcessingMetadata does NOT touch file_hash
//    or lifecycle_state ─────────────────────────────────────────────

// TestQDRANT005_UpdateProcessingMetadata_DoesNotTouchFileHashOrLifecycleState
// asserts that calling UpdateProcessingMetadata leaves file_hash
// (preexisting value), lifecycle_state (preexisting value), and any
// other column unchanged.
func TestQDRANT005_UpdateProcessingMetadata_DoesNotTouchFileHashOrLifecycleState(t *testing.T) {
	repo, db := newTestRepoForQDRANT005(t)
	seedAssetForQDRANT005(t, db, "asset-3", "ACTIVE", "preexisting-hash-xyz")

	if err := repo.UpdateProcessingMetadata(
		context.Background(), "asset-3", "pipeline_stage", "rendering",
	); err != nil {
		t.Fatalf("UpdateProcessingMetadata set: %v", err)
	}

	var lifecycleState, fileHash, metadataJSON string
	if err := db.QueryRow(
		`SELECT lifecycle_state, file_hash, metadata_json FROM media_assets WHERE id = 'asset-3'`,
	).Scan(&lifecycleState, &fileHash, &metadataJSON); err != nil {
		t.Fatalf("select: %v", err)
	}
	if lifecycleState != "ACTIVE" {
		t.Errorf("lifecycle_state = %q, want ACTIVE (UpdateProcessingMetadata must NOT touch lifecycle_state)", lifecycleState)
	}
	if fileHash != "preexisting-hash-xyz" {
		t.Errorf("file_hash = %q, want preexisting-hash-xyz (UpdateProcessingMetadata must NOT touch file_hash)", fileHash)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(metadataJSON), &parsed); err != nil {
		t.Fatalf("metadata_json unmarshal: %v (raw=%q)", err, metadataJSON)
	}
	if got, ok := parsed["pipeline_stage"]; !ok || got != "rendering" {
		t.Errorf("metadata.pipeline_stage = %v, want \"rendering\" (key set)", got)
	}

	// Negative control: deleting the same key (value == nil) must
	// remove it from the JSON map without touching file_hash /
	// lifecycle_state.
	if err := repo.UpdateProcessingMetadata(
		context.Background(), "asset-3", "pipeline_stage", nil,
	); err != nil {
		t.Fatalf("UpdateProcessingMetadata delete: %v", err)
	}
	if err := db.QueryRow(
		`SELECT lifecycle_state, file_hash, metadata_json FROM media_assets WHERE id = 'asset-3'`,
	).Scan(&lifecycleState, &fileHash, &metadataJSON); err != nil {
		t.Fatalf("select after delete: %v", err)
	}
	if lifecycleState != "ACTIVE" || fileHash != "preexisting-hash-xyz" {
		t.Errorf("after delete: lifecycle_state=%q, file_hash=%q, want ACTIVE / preexisting-hash-xyz", lifecycleState, fileHash)
	}
	var parsedAfterDelete map[string]any
	if err := json.Unmarshal([]byte(metadataJSON), &parsedAfterDelete); err != nil {
		t.Fatalf("metadata_json unmarshal after delete: %v", err)
	}
	if _, stillThere := parsedAfterDelete["pipeline_stage"]; stillThere {
		t.Errorf("pipeline_stage still present after delete: %v", parsedAfterDelete)
	}
}

// ── Spec case 4 (positive control): 0 RowsAffected returns error ──

// TestQDRANT005_UpdateFileHash_NotFound asserts the canonical error
// contract: 0 rows affected returns a typed "asset not found" error
// rather than silently succeeding. The error message must mention
// "not found" so callers can branch on intent via errors.Is or
// strings.Contains.
func TestQDRANT005_UpdateFileHash_NotFound(t *testing.T) {
	repo, _ := newTestRepoForQDRANT005(t)
	err := repo.UpdateFileHash(context.Background(), "does-not-exist", "x")
	if err == nil {
		t.Fatalf("expected error on non-existent id, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error message %q must mention \"not found\"", err.Error())
	}
}
