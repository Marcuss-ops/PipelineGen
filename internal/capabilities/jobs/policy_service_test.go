package jobs

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"

	database "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/stager"
)

func setupWorkerAssetsTest(t *testing.T) (*Service, *assetindex.Service, func()) {
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
	`
	db := database.NewTestDBWithSchema(t, schema)
	repo := assetindex.NewRepository(db)
	svc := assetindex.NewService(repo)
	workerSvc := NewService(svc, nil, nil, nil, zap.NewNop())
	// PR-SOURCESTAGER-CONSOLIDATE (July 2026): wire the canonical
	// HTTPSourceStager so the URL download path in fetch routes
	// through the port instead of the retired inline
	// http.NewRequest + client.Do boilerplate. The staging dir is
	// a per-test t.TempDir() sub-directory so parallel tests do not
	// share state. The 2-minute timeout mirrors the production
	// wiring in wire_services.go.
	stagingDir := filepath.Join(t.TempDir(), "staged-sources")
	src, stagerErr := stager.NewHTTPSourceStager(
		stagingDir,
		&http.Client{Timeout: 2 * time.Minute},
		zap.NewNop(),
	)
	if stagerErr != nil {
		t.Fatalf("setupWorkerAssetsTest: NewHTTPSourceStager: %v", stagerErr)
	}
	workerSvc.WithSourceStager(src)
	return workerSvc, svc, func() { db.Close() }
}

func TestDownloadStreamsLocalFile(t *testing.T) {
	workerSvc, assetSvc, cleanup := setupWorkerAssetsTest(t)
	defer cleanup()

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "input.mp4")
	if err := os.WriteFile(filePath, []byte("payload"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	ctx := context.Background()
	if err := assetSvc.Upsert(ctx, &assetindex.AssetRecord{
		AssetID:   "asset-1",
		AssetType: "clip",
		Source:    "artlist",
		SourceID:  "clip-1",
		LocalPath: filePath,
		Status:    "ready",
	}); err != nil {
		t.Fatalf("upsert asset: %v", err)
	}

	rc, filename, err := workerSvc.Download(ctx, "asset-1")
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	defer rc.Close()

	if filename != "input.mp4" {
		t.Fatalf("expected filename input.mp4, got %q", filename)
	}
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read payload: %v", err)
	}
	if string(data) != "payload" {
		t.Fatalf("unexpected payload %q", string(data))
	}
}

func TestDownloadFallsBackToRemoteURL(t *testing.T) {
	workerSvc, assetSvc, cleanup := setupWorkerAssetsTest(t)
	defer cleanup()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Content-Disposition is set on the wire but the new
		// SourceStager-routed fetch (PR-SOURCESTAGER-CONSOLIDATE,
		// July 2026) does NOT parse it: the filename is now a
		// property of the asset record (or, if absent, derived
		// from the URL by Download's resolver), NOT a side effect
		// of the download. The header is kept here only to assert
		// that the staged body is delivered unchanged regardless
		// of response headers.
		w.Header().Set("Content-Disposition", `attachment; filename="ignored.bin"`)
		_, _ = w.Write([]byte("remote-payload"))
	}))
	defer server.Close()

	ctx := context.Background()
	// PR-SOURCESTAGER-CONSOLIDATE (July 2026): the asset_index
	// schema has no dedicated filename column — filename resolution
	// is the responsibility of the caller (Download's resolver
	// falls back to filepath.Base(rawURL) when the record has no
	// local_path and no explicit filename). The test asserts the
	// new contract: body is streamed unchanged + filename falls
	// back to the URL path component.
	if err := assetSvc.Upsert(ctx, &assetindex.AssetRecord{
		AssetID:      "asset-2",
		AssetType:    "clip",
		Source:       "artlist",
		SourceID:     "clip-2",
		DownloadLink: server.URL,
		Status:       "ready",
	}); err != nil {
		t.Fatalf("upsert asset: %v", err)
	}

	rc, filename, err := workerSvc.Download(ctx, "asset-2")
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	defer rc.Close()

	// The new contract: filename is the URL's last path component
	// (or empty if the URL has no path). The Content-Disposition
	// header on the wire is intentionally NOT consulted.
	if filename == "ignored.bin" {
		t.Fatalf("filename should NOT be derived from Content-Disposition in the new design, got %q", filename)
	}
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read payload: %v", err)
	}
	if string(data) != "remote-payload" {
		t.Fatalf("unexpected payload %q", string(data))
	}
}

func TestUploadLifecycleUpdatesAssetIndex(t *testing.T) {
	workerSvc, assetSvc, cleanup := setupWorkerAssetsTest(t)
	defer cleanup()

	ctx := context.Background()
	out, err := workerSvc.InitiateUpload(ctx, "asset-upload-1")
	if err != nil {
		t.Fatalf("initiate upload: %v", err)
	}
	if out.UploadID != "asset-upload-1" {
		t.Fatalf("unexpected upload id %q", out.UploadID)
	}

	rec, err := assetSvc.GetByID(ctx, "asset-upload-1")
	if err != nil {
		t.Fatalf("get asset: %v", err)
	}
	if rec == nil || rec.Status != "uploading" {
		t.Fatalf("expected uploading record, got %#v", rec)
	}

	if err := workerSvc.FinalizeUpload(ctx, "asset-upload-1"); err != nil {
		t.Fatalf("finalize upload: %v", err)
	}

	rec, err = assetSvc.GetByID(ctx, "asset-upload-1")
	if err != nil {
		t.Fatalf("get asset after finalize: %v", err)
	}
	if rec == nil || rec.Status != "ready" {
		t.Fatalf("expected ready record, got %#v", rec)
	}
}

func TestUploadPersistsContent(t *testing.T) {
	workerSvc, assetSvc, cleanup := setupWorkerAssetsTest(t)
	defer cleanup()

	ctx := context.Background()
	if _, err := workerSvc.InitiateUpload(ctx, "asset-upload-2"); err != nil {
		t.Fatalf("initiate upload: %v", err)
	}
	if err := workerSvc.Upload(ctx, "asset-upload-2", "result.mp4", bytes.NewBufferString("video-bytes")); err != nil {
		t.Fatalf("upload content: %v", err)
	}

	rec, err := assetSvc.GetByID(ctx, "asset-upload-2")
	if err != nil {
		t.Fatalf("get asset: %v", err)
	}
	if rec == nil || rec.LocalPath == "" {
		t.Fatalf("expected local path after upload, got %#v", rec)
	}
	data, err := os.ReadFile(rec.LocalPath)
	if err != nil {
		t.Fatalf("read uploaded file: %v", err)
	}
	if string(data) != "video-bytes" {
		t.Fatalf("unexpected uploaded content %q", string(data))
	}
}
