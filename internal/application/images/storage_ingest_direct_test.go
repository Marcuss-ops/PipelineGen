package images

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"go.uber.org/zap"
)

// ── Helpers ───────────────────────────────────────────────────────────

// noopSubjectTags returns empty values for ExtractSubjectAndTags.
type noopSubjectTags struct{}

func (n *noopSubjectTags) ExtractSubjectAndTags(_ context.Context, _ string) (string, []string, error) {
	return "", nil, nil
}

// testImageService creates a minimal ImageStorageService for testing
// ingestDirect. Uses an in-memory SQLite database.
func testImageService(t *testing.T) *ImageStorageService {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	repo := assets.NewImagesRepository(db)

	// Create the minimal schema needed by ImagesRepository.
	for _, stmt := range []string{
		// subjects table (migration 104) — needed by CreateSubject.
		`CREATE TABLE IF NOT EXISTS subjects (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			metadata_json TEXT NOT NULL DEFAULT '{}',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		// media_assets table (migration 033) — needed by AddImage.
		`CREATE TABLE IF NOT EXISTS media_assets (
			id TEXT PRIMARY KEY,
			source TEXT DEFAULT '',
			name TEXT DEFAULT '',
			url TEXT DEFAULT '',
			tags TEXT DEFAULT '[]',
			tags_norm TEXT DEFAULT '',
			media_type TEXT DEFAULT '',
			width INT DEFAULT 0,
			height INT DEFAULT 0,
			file_hash TEXT DEFAULT '',
			local_path TEXT DEFAULT '',
			relative_path TEXT DEFAULT '',
			drive_file_id TEXT DEFAULT '',
			lifecycle_state TEXT DEFAULT 'STAGING',
			metadata_json TEXT DEFAULT '{}',
			origin TEXT DEFAULT '',
			provider TEXT DEFAULT '',
			created_at TEXT DEFAULT '',
			updated_at TEXT DEFAULT ''
		)`,
		// retrieved_image_details (needed by dualWriteImageDetails -> UpsertRetrievedDetails).
		`CREATE TABLE IF NOT EXISTS retrieved_image_details (
			asset_id TEXT PRIMARY KEY,
			source_image_url TEXT DEFAULT '',
			source_page_url TEXT DEFAULT '',
			license TEXT DEFAULT '',
			author TEXT DEFAULT '',
			search_query TEXT DEFAULT '',
			retrieved_at TEXT DEFAULT '',
			provider TEXT DEFAULT ''
		)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("schema setup: %v", err)
		}
	}

	tempDir := t.TempDir()

	// drive.NewResolver takes (mediaRoot, driveRoot string).
	resolver := drive.NewResolver(tempDir, "fake-drive-root")

	// drive.NewStore takes (resolver, rootFolder, imagesFolder, videoAIRoot, soundEffectsRoot string).
	driveStore := drive.NewStore(resolver, "root-folder", "images-folder", "", "")

	return &ImageStorageService{
		repo:        repo,
		mediaStore:  driveStore,
		meta:        &MetadataService{log: zap.NewNop()},
		log:         zap.NewNop(),
		subjectTags: &noopSubjectTags{},
		imagesDir:   tempDir,
	}
}

// ── Tests ─────────────────────────────────────────────────────────────

// TestIngestDirect_TagImageMetadataFailure_IsNonFatal verifies that when
// tagImageMetadata returns an error, ingestDirect does NOT delete the
// local file and does NOT return an error. It continues with metaResult=nil.
//
// PR-QDRANT-IMAGES-INDEX (July 2026): pre-PR this was a hard failure
// (os.Remove + return error). Post-PR it's a warn-and-continue.
func TestIngestDirect_TagImageMetadataFailure_IsNonFatal(t *testing.T) {
	svc := testImageService(t)

	// Wire the Phase 1.2 disabled MetadataWriter stub — Write() returns
	// ErrSemanticMetadataWriterDisabled, which causes tagImageMetadata to fail.
	svc.meta = &MetadataService{
		metaWriter: &semantic.MetadataWriter{},
		log:        zap.NewNop(),
	}

	ctx := context.Background()
	content := []byte{0xFF, 0xD8, 0xFF, 0xE0} // minimal JPEG header
	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	// skipDrive=true avoids real Google Drive upload; skipMetadata=false
	// ensures tagImageMetadata IS called and the failure path is exercised.
	result, err := svc.ingestDirect(ctx, "test-slug", "", "", content, "test.jpg",
		"https://upload.wikimedia.org/wikipedia/test/test.jpg",
		"Test image for unit test", nil, hash, true, false)

	if err != nil {
		t.Fatalf("ingestDirect returned error: %v", err)
	}
	if result == nil {
		t.Fatal("ingestDirect returned nil result")
	}

	// Verify the local file still exists on disk (NOT deleted).
	// Use result.PathRel from the Drive resolver's computed relative path,
	// not a manually constructed path.
	dest := filepath.Join(svc.imagesDir, result.PathRel)
	if _, statErr := os.Stat(dest); os.IsNotExist(statErr) {
		t.Fatalf("local file %s was deleted by ingestDirect (pre-PR os.Remove behavior)", dest)
	}
}

// TestIngestDirect_DispatcherNil_NoPanic verifies that when
// s.dispatcher is nil, the function does NOT panic and returns
// successfully.
func TestIngestDirect_DispatcherNil_NoPanic(t *testing.T) {
	svc := testImageService(t)
	svc.dispatcher = nil // explicitly nil

	ctx := context.Background()
	content := []byte{0xFF, 0xD8, 0xFF, 0xE0}
	hash := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	result, err := svc.ingestDirect(ctx, "test-slug", "", "", content, "test.jpg",
		"https://upload.wikimedia.org/wikipedia/test/test.jpg",
		"Test for nil dispatcher guard", nil, hash, true, true)

	if err != nil {
		t.Fatalf("ingestDirect with nil dispatcher returned error: %v", err)
	}
	if result == nil {
		t.Fatal("ingestDirect with nil dispatcher returned nil result")
	}
}

// TestIngestDirect_EnqueueAndIndexCalled is an integration-level test
// that verifies EnqueueAndIndex IS called after AddImage when a real
// dispatcher is wired. Requires a live SQLite-backed outbox.Dispatcher.
//
// Forward-pointer PR-QDRANT-IMAGES-ENQUEUE-TEST (deadline 2026-08-15):
// wire a real outbox.Dispatcher with temp SQLite and verify
// outbox_events table has an asset.index.requested row with
// aggregate_id = hash after ingestDirect succeeds.
func TestIngestDirect_EnqueueAndIndexCalled(t *testing.T) {
	t.Skip("integration test — requires real outbox.Dispatcher with SQLite-backed outbox events repo (forward-pointer PR-QDRANT-IMAGES-ENQUEUE-TEST)")
}
