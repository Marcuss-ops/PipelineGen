package renderinggen

import (
	"context"
	"fmt"
	"os"
	"strings"

	renderqueue "github.com/Marcuss-ops/PipelineGen/internal/kernel/script/renderqueue"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// OverlayClipFetcher materializes one certified overlay item render into a
// local file, verifying the certified size + digest while streaming.
//
// It exists so the local join can place the EXACT pixels the renderer
// certified on the timeline: the object store is the only authority for those
// bytes, and a re-encode from any other source would not be the same artifact.
type OverlayClipFetcher struct {
	storeURL string
}

// NewOverlayClipFetcher builds the fetcher for one object store.
func NewOverlayClipFetcher(storeURL string) *OverlayClipFetcher {
	return &OverlayClipFetcher{storeURL: strings.TrimRight(strings.TrimSpace(storeURL), "/")}
}

// FetchOverlayClip downloads artifact into destPath.
//
// The artifact locator is preferred; a bare storage key is resolved against the
// configured store. An artifact with neither is a producer defect, not a
// transient miss, so it fails closed instead of writing an empty file.
func (f *OverlayClipFetcher) FetchOverlayClip(ctx context.Context, artifact renderqueue.RenderArtifact, destPath string) error {
	target := strings.TrimSpace(artifact.URL)
	if target == "" && strings.TrimSpace(artifact.StorageKey) != "" {
		store := f.storeURL
		if store == "" {
			store = defaultStoreURL()
		}
		target = store + "/objects/" + strings.TrimSpace(artifact.StorageKey)
	}
	if target == "" {
		return fmt.Errorf("overlay clip %q carries no artifact locator", artifact.ID)
	}
	if _, _, err := materializeArtifact(ctx, target, destPath, artifact.SizeBytes, artifact.SHA256); err != nil {
		return err
	}
	if info, err := os.Stat(destPath); err != nil || info.Size() == 0 {
		return fmt.Errorf("overlay clip %q materialized no bytes at %s", artifact.ID, destPath)
	}
	return nil
}

func defaultStoreURL() string {
	if store := strings.TrimSpace(os.Getenv("RENDERINGGEN_STORE_URL")); store != "" {
		return strings.TrimRight(store, "/")
	}
	return config.LoopbackURLPrefix + "9000"
}
