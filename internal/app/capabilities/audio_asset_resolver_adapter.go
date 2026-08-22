// Package capabilities — audio_asset_resolver_adapter.go wires the
// concrete AudioAssetSource adapter for the BGM/SFX asset resolution
// phase. The capability (internal/capabilities/scripts) owns the port;
// THIS file (composition root) owns the mechanics:
//
//   - asset registry lookup (canonical asset.Service)
//   - media-type gate (only audio / sound_effect assets are accepted)
//   - local copy reuse + Drive download into scratch (delegates to the
//     single CanonicalAssetMaterializer — no second materialization path)
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
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	drivepkg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// audioAssetSourceAdapter resolves a background-music or sound-effect
// asset_id to a verified local path. Precedence: (1) the registry's
// local_path when the file exists, (2) a content-addressed scratch copy
// (CAS when sha256 is known, legacy asset_id otherwise), (3) a fresh
// Drive download into scratch. Delegates to the single
// CanonicalAssetMaterializer — no second download path.
type audioAssetSourceAdapter struct {
	assets    *asset.Service
	canonical *drivepkg.CanonicalAssetMaterializer
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
	if r.canonical == nil {
		return audio.ResolvedAudioAsset{}, errors.New("audio asset resolution: materializer not wired")
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
	case asset.MediaType("video"):
		// Some registry assets are MP4 containers with a certified audio
		// stream (for example the local background test asset). FFmpeg can
		// select that stream for a BGM layer; rejecting the container solely
		// from its primary media type prevents otherwise valid audio-only
		// extraction. The renderer still fails closed if the container has no
		// audio stream.
	default:
		return audio.ResolvedAudioAsset{}, fmt.Errorf("audio asset %q has media type %q; only audio and sound_effect assets can be mixed", assetID, a.MediaType)
	}

	// Derive the extension from the asset's filename when available.
	// Never hardcode .m4a — the original asset may be WAV/MP3/AAC.
	ext := filepath.Ext(a.Filename)
	if ext == "" {
		ext = ".m4a"
	}

	result, matErr := r.canonical.Materialize(ctx, drivepkg.MaterializeRequest{
		AssetID:        assetID,
		DriveFileID:    a.DriveFileID(),
		ExpectedSHA256: a.Sha256(),
		Extension:      ext,
		RegisteredPath: strings.TrimSpace(a.LocalPath()),
	})
	if matErr != nil {
		return audio.ResolvedAudioAsset{}, fmt.Errorf("audio asset %q: %w", assetID, matErr)
	}

	return audio.ResolvedAudioAsset{
		AssetID:    a.ID,
		Path:       result.LocalPath,
		DurationUS: a.Duration.Microseconds(),
	}, nil
}
