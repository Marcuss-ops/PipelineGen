package wiring

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// ResolveFinalJobObjectStoreAsset publishes Creator-local media bytes into the
// shared Objectstore and returns a signed worker source. In direct mode it
// deliberately does not fall back to a Drive link: missing local bytes stop
// submission before Velox can dispatch an incomplete job.
func (a *remoteFinalJobAdapter) ResolveFinalJobObjectStoreAsset(ctx context.Context, id string) (map[string]any, error) {
	for _, editorial := range mediaregistry.EditorialAudioAssets() {
		if editorial.Alias != strings.TrimSpace(id) || editorial.Family != "music" {
			continue
		}
		path := filepath.Join("data", "media", "sound_effects", editorial.Filename)
		sha, size, err := digest.SHA256File(path)
		if err != nil {
			return nil, fmt.Errorf("read Creator-local BGM %s: %w", editorial.Alias, err)
		}
		return scriptgen.PublishFinalJobLocalObject(ctx, editorial.Alias, path, sha, size)
	}
	if a == nil || a.media == nil {
		return nil, fmt.Errorf("direct Objectstore source requires the Creator media registry")
	}
	mediaAsset, err := a.media.ResolveByMediaAssetID(ctx, id)
	if err != nil {
		return nil, err
	}
	if mediaAsset == nil {
		assets, resolveErr := a.media.ResolveByDriveFileID(ctx, id)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if len(assets) > 0 {
			mediaAsset = assets[0]
		}
	}
	if mediaAsset == nil {
		return nil, fmt.Errorf("asset %q is absent from the Creator media registry", id)
	}
	sha := strings.TrimSpace(mediaAsset.BinarySHA256())
	if len(sha) != 64 {
		return nil, fmt.Errorf("asset %q has no canonical SHA-256", id)
	}
	path := strings.TrimSpace(mediaAsset.LocalPath())
	if path == "" {
		return nil, fmt.Errorf("asset %q has no Creator-local bytes; direct Objectstore mode does not pass Drive references to Velox", id)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("asset %q Creator-local source %q: %w", id, path, err)
	}
	ref, err := scriptgen.PublishFinalJobLocalObject(ctx, mediaAsset.ID, path, sha, info.Size())
	if err != nil {
		return nil, fmt.Errorf("publish asset %q to Objectstore: %w", id, err)
	}
	durationMS := mediaAsset.Duration.Milliseconds()
	if durationMS <= 0 {
		durationMS = int64(mediaAsset.LegacyDurationMSMirror())
	}
	if durationMS > 0 {
		ref["duration_ms"] = durationMS
	}
	return ref, nil
}
