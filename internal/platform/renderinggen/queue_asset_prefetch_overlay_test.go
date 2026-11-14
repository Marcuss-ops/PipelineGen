package renderinggen

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
)

func TestPrefetchClipAssetsRejectsUnverifiedOverlayLocalBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "overlay.mp4")
	if err := os.WriteFile(path, []byte("these are not the declared bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	declared := sha256.Sum256([]byte("the bytes the overlay plan promised"))
	ref := assetRef{
		Hash:        hex.EncodeToString(declared[:]),
		LogicalPath: "overlay-test.mp4",
		LocalPath:   path,
	}

	err := prefetchClipAssets(context.Background(), cliprender.ClipRenderPlanV1{}, []assetRef{ref})
	if !errors.Is(err, ErrAssetSourceUnavailable) {
		t.Fatalf("prefetch error = %v, want ErrAssetSourceUnavailable for an overlay whose local bytes do not match its content address", err)
	}
}
