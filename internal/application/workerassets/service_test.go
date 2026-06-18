package workerassets

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/media/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/storage"
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
		updated_at TEXT NOT NULL
	);
	`
	db := storage.NewTestDBWithSchema(t, schema)
	repo := assetindex.NewRepository(db)
	svc := assetindex.NewService(repo)
	workerSvc := NewService(svc, nil, nil, nil, zap.NewNop())
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
		w.Header().Set("Content-Disposition", `attachment; filename="remote.bin"`)
		_, _ = w.Write([]byte("remote-payload"))
	}))
	defer server.Close()

	ctx := context.Background()
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

	if filename != "remote.bin" {
		t.Fatalf("expected filename remote.bin, got %q", filename)
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
