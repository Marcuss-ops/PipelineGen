package stockpipeline

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
)

type retryRemoveFS struct {
	LocalFSPort
	calls int
}

func (f *retryRemoveFS) RemoveAll(path string) error {
	f.calls++
	if f.calls == 1 {
		return errors.New("synthetic remove failure")
	}
	return f.LocalFSPort.RemoveAll(path)
}

func TestCleanupSharedLease_RetainsRetryAfterRemoveFailure(t *testing.T) {
	base := testFS
	fs := &retryRemoveFS{LocalFSPort: base}
	stager, _, _ := setupTestEnv(t, newFakeDownloader([]byte("retryable")))
	stager.svc.localFS = fs

	root := t.TempDir()
	ownerPath := filepath.Join(root, "owner", "source.mp4")
	leaderPath := filepath.Join(root, "leader", "source.mp4")
	lease, _ := stager.reserveSharedLease("retry-key", ownerPath)
	stager.publishSharedLease(lease, leaderPath, true)
	staged := &assets.StagedAsset{LocalPath: ownerPath}

	if err := stager.cleanup(context.Background(), staged); err == nil {
		t.Fatal("expected first cleanup to report RemoveAll failure")
	}
	if _, ok := stager.sharedRefs.Load("retry-key"); !ok {
		t.Fatal("lease was discarded after failed cleanup")
	}
	if _, ok := stager.assetLeases.Load(staged.LocalPath); !ok {
		t.Fatal("asset lease binding was not restored after failed cleanup")
	}
	if err := stager.cleanup(context.Background(), staged); err != nil {
		t.Fatalf("retry cleanup failed: %v", err)
	}
	if _, ok := stager.sharedRefs.Load("retry-key"); ok {
		t.Fatal("lease remained after successful retry")
	}
}

var _ LocalFSPort = (*retryRemoveFS)(nil)
