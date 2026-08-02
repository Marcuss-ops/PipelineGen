package images

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	persistence "github.com/Marcuss-ops/PipelineGen/internal/application/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/imagesrepo"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

// ── Helpers ───────────────────────────────────────────────────────────

// noopSubjectTags returns empty values for ExtractSubjectAndTags.
type noopSubjectTags struct{}

func (n *noopSubjectTags) ExtractSubjectAndTags(_ context.Context, _ string) (string, []string, error) {
	return "", nil, nil
}

type fakePublisher struct {
	fileID string
	link   string
}

func (f *fakePublisher) Publish(_ context.Context, _ delivery.PublishRequest) (*delivery.PublishResult, error) {
	return &delivery.PublishResult{FileID: f.fileID, WebViewLink: f.link}, nil
}

func (f *fakePublisher) ResolveFolder(_ context.Context, _ delivery.PublishRequest) (string, error) {
	return "folder-1", nil
}

// recordingCommitter is the unit-test stub for persistence.AssetCommitter
// used by the happy-path tests. It records the call args (so tests can
// assert the canonical CommitRequest shape) and returns a successful
// CommitResult. Tests that intentionally need a nil Committer
// (e.g. TestIngestDirect_CommitterNil_FailsClosed) set svc.committer
// = nil directly.
type recordingCommitter struct {
	calls      int
	lastReq    persistence.AssetCommitRequest
	lastResult persistence.CommittedAsset
}

func (r *recordingCommitter) CommitTx(_ context.Context, _ persistence.Transaction, req persistence.AssetCommitRequest) (persistence.CommitResult, error) {
	r.calls++
	r.lastReq = req
	out := r.success(req)
	r.lastResult = out
	return out, nil
}

func (r *recordingCommitter) CommitAndIndex(_ context.Context, req persistence.AssetCommitRequest) (persistence.CommitResult, error) {
	r.calls++
	r.lastReq = req
	out := r.success(req)
	r.lastResult = out
	return out, nil
}

func (r *recordingCommitter) CommitAsset(_ context.Context, req persistence.AssetCommitRequest) (persistence.CommittedAsset, error) {
	r.calls++
	r.lastReq = req
	out := r.success(req)
	r.lastResult = out
	return out, nil
}

func (r *recordingCommitter) success(req persistence.AssetCommitRequest) persistence.CommitResult {
	return persistence.CommitResult{
		AssetRowsAffected: 1,
		OutboxInserted:    req.EmitIndexEvent,
		OutboxEventKey:    req.AssetID + ":" + req.ContentHash,
	}
}

// testImageService creates a minimal ImageStorageService for testing ingestDirect.
func testImageService(t *testing.T) *ImageStorageService {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	repo := imagesrepo.NewImagesRepository(db)

	// Create the minimal schema needed by ImagesRepository for the
	// pre-Commit flows (subjects upsert). The post-CommitAsset path
	// is exercised against the recordingCommitter stub, so the
	// production media_assets / asset_locations / outbox_events
	// schema is NOT required in this in-memory test DB.
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
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("schema setup: %v", err)
		}
	}

	tempDir := t.TempDir()

	return &ImageStorageService{
		repo:        repo,
		meta:        &MetadataService{log: zap.NewNop()},
		log:         zap.NewNop(),
		subjectTags: &noopSubjectTags{},
		imagesDir:   tempDir,
		committer:   &recordingCommitter{},
	}
}

// ── Tests ─────────────────────────────────────────────────────────────

// TestIngestDirect_TagImageMetadataFailure_IsNonFatal verifies that when
// tagImageMetadata returns an error, ingestDirect does NOT delete the
// local file and does NOT return an error. It continues with metaResult=nil.
//
// PR-QDRANT-IMAGES-INDEX (July 2026): pre-PR this was a hard failure
// (os.Remove + return error). Post-PR it's a warn-and-continue.
//
// PR-IMAGES-INGEST-ATOMIC (July 2026): now routed through CommitAsset
// via the recording stub, so the happy path exercises the canonical
// atomic write instead of the legacy AddImage + EnqueueAndIndex path.
//
// PR-IMAGES-REMOVE-DRIVE-STORE (July 2026): local path computation is
// inline (slug+ext), no drive.Store dependency in testImageService.
func TestIngestDirect_TagImageMetadataFailure_IsNonFatal(t *testing.T) {
	svc := testImageService(t)
	committer := svc.committer.(*recordingCommitter)

	// Wire the Phase 1.2 disabled MetadataWriter stub — Write() returns
	// ErrSemanticMetadataWriterDisabled, which causes tagImageMetadata to fail.
	svc.meta = &MetadataService{
		metaWriter: semantic.NewNopMetadataWriter(zap.NewNop()), // P0-#2: nop implementation of the MetadataWriterPort
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

	// Verify the local file was written (inline path computation: slug+ext).
	dest := filepath.Join(svc.imagesDir, result.PathRel)
	if _, statErr := os.Stat(dest); os.IsNotExist(statErr) {
		t.Fatalf("local file %s missing (ingestDirect must write the image to local disk)", dest)
	}

	// PR-IMAGES-INGEST-ATOMIC: verify CommitAsset was called EXACTLY once
	// with EmitIndexEvent=true (the SSOT contract for image ingest —
	// outbox event emitted atomically with the media_assets row).
	if committer.calls != 1 {
		t.Errorf("recordingCommitter.calls = %d, want 1 (PR-IMAGES-INGEST-ATOMIC: CommitAsset must be called exactly once)", committer.calls)
	}
	if !committer.lastReq.EmitIndexEvent {
		t.Error("CommitAsset called with EmitIndexEvent=false; image ingest MUST emit the asset.index.requested outbox event atomically")
	}
	if committer.lastReq.AssetID != hash || committer.lastReq.ContentHash != hash {
		t.Errorf("CommitAsset AssetID/ContentHash = (%q, %q); both must equal the content hash to preserve identity", committer.lastReq.AssetID, committer.lastReq.ContentHash)
	}
}

// TestIngestDirect_CommitterNil_FailsClosed verifies that when
// s.committer is nil, ingestDirect returns a typed error and does NOT
// write the corresponding outbox event. It is the post-refactor mirror
// of TestIngestDirect_DispatcherNil_NoPanic: the prior fail-open
// dispatcher guard (which silently dropped index writes) is replaced
// by a fail-closed committer guard (which refuses to write the SQLite
// half of the legacy 2-transaction pipeline).
//
// godlike/07 NO-FAKE-AVAILABILITY: a nil Committer indicating "no
// index writer available" must surface as a typed error so callers can
// choose to skip the index write or treat it as fatal. Refusing to
// write a (partial) media_assets row without the matching
// outbox event is the SSOT — it preserves the atomicity invariant
// the refactor closes.
func TestIngestDirect_CommitterNil_FailsClosed(t *testing.T) {
	svc := testImageService(t)
	svc.committer = nil // explicit nil — fail-closed assertion

	ctx := context.Background()
	content := []byte{0xFF, 0xD8, 0xFF, 0xE0}
	hash := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	_, err := svc.ingestDirect(ctx, "test-slug", "", "", content, "test.jpg",
		"https://upload.wikimedia.org/wikipedia/test/test.jpg",
		"Test for nil committer guard", nil, hash, true, true)

	if err == nil {
		t.Fatal("ingestDirect with nil committer returned nil error; MUST fail closed (godlike/07 NO-FAKE-AVAILABILITY)")
	}
	// The error message must signal the SSOT contract so operators can
	// route on it: refuse to write the SQLite half of the legacy
	// 2-transaction pipeline without the matching outbox event.
	if !errors.Is(err, errImageIngestCommitterNil) {
		t.Errorf("error %v is not errors.Is errImageIngestCommitterNil (godlike/07 typed-sentinel contract)", err)
	}
	if !strings.Contains(err.Error(), "asset committer is nil") {
		t.Errorf("error = %q, want message containing 'asset committer is nil' (godlike/07 NO-FAKE-AVAILABILITY contract)", err.Error())
	}
}

// TestIngestDirect_GeneratedImage_StoresDriveLinkAsSourceURL verifies that
// generated images persist the Drive web link as the canonical SourceURL
// once upload succeeds. The provider label still flows into Provider.
//
// PR-IMAGES-INGEST-ATOMIC (July 2026): the recordingCommitter stub is
// wired to record the canonical CommitRequest shape.
//
// PR-IMAGES-REMOVE-DRIVE-STORE (July 2026): s.publisher.Publish is now
// called directly from ingestDirect (no more publishToDrive bridge).
func TestIngestDirect_GeneratedImage_StoresDriveLinkAsSourceURL(t *testing.T) {
	svc := testImageService(t)
	committer := svc.committer.(*recordingCommitter)
	svc.cfg = &config.Config{Drive: config.DriveConfig{ImagesRootFolder: "images-root"}}
	svc.publisher = &fakePublisher{
		fileID: "drive-file-123",
		link:   "https://drive.google.com/file/d/drive-file-123/view",
	}

	ctx := context.Background()
	content := []byte{0xFF, 0xD8, 0xFF, 0xE0}
	hash := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

	result, err := svc.ingestDirect(
		ctx,
		"test-slug",
		"",
		"",
		content,
		"test.jpg",
		"google-slides",
		"Generated image for test",
		nil,
		hash,
		false,
		false,
	)
	if err != nil {
		t.Fatalf("ingestDirect returned error: %v", err)
	}
	if result == nil {
		t.Fatal("ingestDirect returned nil result")
	}
	if result.SourceURL != "https://drive.google.com/file/d/drive-file-123/view" {
		t.Fatalf("SourceURL = %q, want drive web link", result.SourceURL)
	}
	if result.Provider != "google-slides" {
		t.Fatalf("Provider = %q, want google-slides", result.Provider)
	}

	// PR-IMAGES-INGEST-ATOMIC: verify CommitAsset was called exactly
	// once and routed the SourceURL (Drive link) through to the
	// canonical CommitRequest.
	if committer.calls != 1 {
		t.Errorf("recordingCommitter.calls = %d, want 1", committer.calls)
	}
	if committer.lastReq.SourceURL != "https://drive.google.com/file/d/drive-file-123/view" {
		t.Errorf("CommitAsset SourceURL = %q, want drive web link", committer.lastReq.SourceURL)
	}
}

func TestIngestDirect_RetrievedURLPreservesResolvedProvider(t *testing.T) {
	svc := testImageService(t)
	committer := svc.committer.(*recordingCommitter)
	ctx := context.WithValue(context.Background(), RetrieverKey, "duckduckgo")
	content := []byte{0xFF, 0xD8, 0xFF, 0xE0}
	hash := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"

	result, err := svc.ingestDirect(
		ctx,
		"ddg-test",
		"",
		"",
		content,
		"test.jpg",
		"https://images.example/fox.jpg",
		"Retrieved image for provider test",
		nil,
		hash,
		true,
		true,
	)
	if err != nil {
		t.Fatalf("ingestDirect returned error: %v", err)
	}
	if result.Provider != "duckduckgo" || result.Origin != "retrieved" {
		t.Fatalf("result provenance = provider=%q origin=%q, want duckduckgo/retrieved", result.Provider, result.Origin)
	}
	if got := committer.lastReq.Metadata.SourceProvider; got != "duckduckgo" {
		t.Fatalf("CommitAsset SourceProvider = %q, want duckduckgo", got)
	}
}

// TestIngestDirect_CommitAsset_AtomicSSOTContract pins the SSOT
// invariant that the legacy 2-transaction crash-window is closed:
// CommitAsset is the only canonical producer of (media_assets row,
// asset.index.requested outbox event) pairs.
//
// Forward-pointer PR-QDRANT-IMAGES-INTEGRATION-TEST (deadline 2026-08-15):
// wire a real NewSQLiteAssetCommitter against a temp SQLite DB to
// verify outbox_events table is populated in the same transaction
// as media_assets (the full atomicity invariant that the legacy
// runtime only enforced via the runtime reconcile tool).
func TestIngestDirect_CommitAsset_AtomicSSOTContract(t *testing.T) {
	svc := testImageService(t)
	committer := svc.committer.(*recordingCommitter)

	ctx := context.Background()
	content := []byte{0xFF, 0xD8, 0xFF, 0xE0}
	hash := "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"

	_, err := svc.ingestDirect(ctx, "test-slug", "", "", content, "test.jpg",
		"https://upload.wikimedia.org/wikipedia/test/test.jpg",
		"SSOT contract check — atomic commit", nil, hash, true, true)
	if err != nil {
		t.Fatalf("ingestDirect returned error: %v", err)
	}
	if committer.calls != 1 {
		t.Errorf("recordingCommitter.calls = %d, want exactly 1 (closes the legacy 2-tx crash window)", committer.calls)
	}
	if !committer.lastReq.EmitIndexEvent {
		t.Error("CommitAsset called with EmitIndexEvent=false; image ingest SSOT requires EmitIndexEvent=true so the outbox row + media_assets row commit atomically")
	}
}
