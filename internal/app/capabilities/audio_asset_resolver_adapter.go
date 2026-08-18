// Package capabilities — audio_asset_resolver_adapter.go wires the
// concrete AudioAssetSource adapter for the BGM/SFX asset resolution
// phase. The capability (internal/capabilities/scripts) owns the port;
// THIS file (composition root) owns the mechanics:
//
//   - asset registry lookup (canonical asset.Service)
//   - media-type gate (only audio / sound_effect assets are accepted)
//   - local copy reuse + Drive download into scratch
//
// Mirrors the clip.render resolver/materializer pair
// (cliprender_adapters.go): the same registry and Drive sources feed both
// phases, and every failure mode is fail-closed — an unknown asset_id, a
// non-audio asset, or an asset with no local copy and no Drive source is
// a typed error, never a silent no-op path.
package capabilities

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	drivepkg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// audioAssetSourceAdapter resolves a background-music or sound-effect
// asset_id to a verified local path. Precedence: (1) the registry's
// local_path when the file exists, (2) a scratch copy already downloaded
// in a prior run (idempotent per asset_id), (3) a fresh Drive download
// into scratch.
type audioAssetSourceAdapter struct {
	assets     *asset.Service
	drive      drivepkg.Reader
	scratchDir string
}

// ResolveAudioAsset implements scripts.AudioAssetSource. Only
// audio/sound_effect assets are accepted: handing a video or image path
// to the audio renderer would fail late and obscurely, so the gate is
// applied here at resolution time. The certified source duration from the
// registry is carried on the resolved asset so the loop expander can make
// deterministic decisions in Go.
func (r *audioAssetSourceAdapter) ResolveAudioAsset(ctx context.Context, assetID string) (audio.ResolvedAudioAsset, error) {
	if r == nil || r.assets == nil {
		return audio.ResolvedAudioAsset{}, errors.New("audio asset resolution: asset registry not wired")
	}
	details, err := r.assets.Get(ctx, assetID)
	if err != nil {
		return audio.ResolvedAudioAsset{}, fmt.Errorf("load audio asset %q: %w", assetID, err)
	}
	if details == nil || details.Asset == nil {
		return audio.ResolvedAudioAsset{}, fmt.Errorf("audio asset %q not found", assetID)
	}
	a := details.Asset
	switch a.MediaType {
	case asset.MediaTypeAudio, asset.MediaTypeSoundEffect:
	default:
		return audio.ResolvedAudioAsset{}, fmt.Errorf("audio asset %q has media type %q; only audio and sound_effect assets can be mixed", assetID, a.MediaType)
	}

	// (1) Registered local copy.
	if local := strings.TrimSpace(a.LocalPath()); local != "" {
		if info, err := os.Stat(local); err == nil && !info.IsDir() {
			return resolvedAudioAsset(a, local), nil
		}
	}

	// (2/3) Drive materialization into scratch.
	if a.DriveFileID() == "" {
		return audio.ResolvedAudioAsset{}, fmt.Errorf("audio asset %q has neither a local copy nor a Drive source", assetID)
	}
	if r.drive == nil {
		return audio.ResolvedAudioAsset{}, fmt.Errorf("audio asset resolution: Drive reader not wired (asset %q requires Drive materialization)", assetID)
	}
	target := filepath.Join(r.scratchDir, "assets", assetID+".m4a")
	if info, err := os.Stat(target); err == nil && !info.IsDir() {
		return resolvedAudioAsset(a, target), nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return audio.ResolvedAudioAsset{}, fmt.Errorf("create audio scratch dir: %w", err)
	}
	rc, _, err := r.drive.DownloadFile(ctx, a.DriveFileID())
	if err != nil {
		return audio.ResolvedAudioAsset{}, fmt.Errorf("download audio asset %q from Drive: %w", assetID, err)
	}
	defer rc.Close()

	out, err := os.Create(target)
	if err != nil {
		return audio.ResolvedAudioAsset{}, fmt.Errorf("create scratch file for audio asset %q: %w", assetID, err)
	}
	n, copyErr := io.Copy(out, rc)
	closeErr := out.Close()
	if copyErr != nil {
		return audio.ResolvedAudioAsset{}, fmt.Errorf("write scratch file for audio asset %q: %w", assetID, copyErr)
	}
	if closeErr != nil {
		return audio.ResolvedAudioAsset{}, fmt.Errorf("close scratch file for audio asset %q: %w", assetID, closeErr)
	}
	if n <= 0 {
		return audio.ResolvedAudioAsset{}, fmt.Errorf("audio asset %q downloaded empty", assetID)
	}
	return resolvedAudioAsset(a, target), nil
}

// resolvedAudioAsset projects the registry asset onto the canonical
// resolved shape with its certified duration (0 when the registry has no
// duration recorded — loop expansion fails closed on that).
func resolvedAudioAsset(a *asset.Asset, path string) audio.ResolvedAudioAsset {
	return audio.ResolvedAudioAsset{
		AssetID:    a.ID,
		Path:       path,
		DurationUS: a.Duration.Microseconds(),
	}
}
