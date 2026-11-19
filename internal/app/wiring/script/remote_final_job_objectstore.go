package script

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	drivepkg "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	"go.uber.org/zap"
)

// ResolveFinalJobObjectStoreAsset publishes Creator-local media bytes into the
// shared Objectstore and returns a signed worker source. If the registered
// local copy is missing, it restores the content-verified bytes from the
// asset's canonical Drive identity first.
func (a *RemoteFinalJobAdapter) ResolveFinalJobObjectStoreAsset(ctx context.Context, id string) (map[string]any, error) {
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
	// A registered local_path can outlive its file (for example after media
	// cleanup). Treat a missing path like an empty one: if this asset still has
	// a Drive identity, restore its verified bytes before handing it to the
	// Objectstore publisher.
	if path != "" {
		if info, statErr := os.Stat(path); statErr != nil || !info.Mode().IsRegular() {
			path = ""
		}
	}
	driveID := strings.TrimSpace(mediaAsset.DriveFileID())
	if driveID == "" {
		driveID = driveFileIDFromLink(mediaAsset.DriveLink())
	}
	if path == "" && a.stock != nil && driveID != "" {
		ext := filepath.Ext(mediaAsset.Filename)
		if ext == "" {
			ext = ".mp4"
		}
		mat, matErr := drivepkg.NewCanonicalAssetMaterializer(a.stock, "scratch", zap.NewNop())
		if matErr != nil {
			return nil, fmt.Errorf("asset %q materializer init: %w", id, matErr)
		}
		res, matErr := mat.Materialize(ctx, drivepkg.MaterializeRequest{
			AssetID:        mediaAsset.ID,
			DriveFileID:    driveID,
			ExpectedSHA256: sha,
			Extension:      ext,
			RegisteredPath: strings.TrimSpace(mediaAsset.LocalPath()),
		})
		if matErr != nil {
			return nil, fmt.Errorf("asset %q materialize from Drive: %w", id, matErr)
		}
		path = res.LocalPath
	}
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

// driveFileIDFromLink extracts the identity from canonical Google Drive file
// links. We only use it as a fallback when the media row's dedicated
// drive_file_id field is empty; arbitrary URLs are never fetched as media.
func driveFileIDFromLink(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(u.Hostname(), "drive.google.com") {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "d" && parts[i+1] != "" {
			return parts[i+1]
		}
	}
	if strings.Trim(u.Path, "/") == "open" || strings.Trim(u.Path, "/") == "uc" {
		return strings.TrimSpace(u.Query().Get("id"))
	}
	return ""
}
