package artlist

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/acquisition"
)

type fakeStagerDownloader struct {
	path string
}

func (f *fakeStagerDownloader) Download(_ context.Context, req DownloadRequest) (*DownloadResult, error) {
	if err := os.MkdirAll(req.DestinationID, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(req.DestinationID, req.Filename)
	if err := os.WriteFile(path, []byte("artlist-stage"), 0o600); err != nil {
		return nil, err
	}
	f.path = path
	return &DownloadResult{LocalPath: path, Bytes: int64(len("artlist-stage"))}, nil
}

func TestArtlistStager_PrepareRelease_UsesPrivateStageAndTypedLifecycle(t *testing.T) {
	dl := &fakeStagerDownloader{}
	stager := NewArtlistStager(dl)
	req := acquisition.PrepareRequest{
		Source: acquisition.SourceRef{URL: "https://cdn.artlist.io/source.mp4", PolicyVersion: "test"},
	}

	prepared, err := stager.Prepare(context.Background(), req)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if prepared == nil || prepared.LocalPath == "" || prepared.CleanupToken == "" {
		t.Fatalf("Prepare returned incomplete receipt: %+v", prepared)
	}
	if filepath.Dir(prepared.LocalPath) == os.TempDir() {
		t.Fatalf("stage must use a private directory, got %q", prepared.LocalPath)
	}
	if _, err := os.Stat(prepared.LocalPath); err != nil {
		t.Fatalf("staged file missing: %v", err)
	}

	cached, err := stager.Prepare(context.Background(), req)
	if err != nil {
		t.Fatalf("idempotent Prepare: %v", err)
	}
	if cached.LocalPath != prepared.LocalPath {
		t.Fatalf("idempotent Prepare path = %q, want %q", cached.LocalPath, prepared.LocalPath)
	}

	if err := stager.Release(context.Background(), prepared.CleanupToken); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := os.Stat(prepared.LocalPath); !os.IsNotExist(err) {
		t.Fatalf("released file still exists, stat err=%v", err)
	}
	if err := stager.Release(context.Background(), prepared.CleanupToken); !errors.Is(err, acquisition.ErrAcquisitionAlreadyReleased) {
		t.Fatalf("second Release error = %v, want ErrAcquisitionAlreadyReleased", err)
	}
}

func TestArtlistStager_ReleaseUnknownTokenFailsClosed(t *testing.T) {
	stager := NewArtlistStager(&fakeStagerDownloader{})
	if err := stager.Release(context.Background(), "unknown"); !errors.Is(err, acquisition.ErrAcquisitionInvalidToken) {
		t.Fatalf("Release unknown token = %v, want ErrAcquisitionInvalidToken", err)
	}
}
