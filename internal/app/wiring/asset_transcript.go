package wiring

// asset_transcript.go — composition for GET /api/clips/transcript?asset_id=...
//
// The handler must not own media/ASR mechanics. This file composes the same
// clip.render ports the render path already uses:
//   - PostgreSQL media resolver (asset_id -> AssetRef);
//   - canonical materializer (local copy / Drive);
//   - ClipRenderTranscriptResolver (READY track reuse + streaming Whisper +
//     persistence);
//   - canonical text-track repository (fast path).
//
// Missing optional dependencies downgrade only the ASR fallback: a READY text
// track still answers. A missing text-track repository leaves the whole asset
// route at 503, because there is no honest source left.

import (
	clipswiring "github.com/Marcuss-ops/PipelineGen/internal/app/modules/clips"
	youtubeapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/youtube"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	clipadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	drivepkg "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	"go.uber.org/zap"
)

// buildYouTubeAssetTranscriptSource returns nil when the text-track repository
// is unavailable. That is the route's fail-closed 503 state, not a silent
// YouTube fallback.
func buildYouTubeAssetTranscriptSource(cfg *config.Config, root *ComposeRoot, log *zap.Logger) youtubeapi.AssetTranscriptSource {
	if cfg == nil || root == nil || root.Repos == nil || root.Repos.TextTrackRepo == nil {
		return nil
	}
	if log == nil {
		log = zap.NewNop()
	}

	var assetResolver cliprender.AssetResolver
	if root.MediaPostgres != nil {
		resolver, err := clipswiring.NewClipRenderMediaResolver(root.MediaPostgres, log)
		if err != nil {
			log.Warn("asset transcript: media resolver unavailable; ASR fallback disabled (READY text tracks still work)", zap.Error(err))
		} else {
			assetResolver = resolver
		}
	}

	var materializer cliprender.AssetMaterializer
	var driveReader drivepkg.Reader
	if root.Drive != nil {
		driveReader = root.Drive.Reader
	}
	if driveReader != nil {
		m, err := clipadapters.NewClipRenderMaterializer(driveReader, clipswiring.AssetMaterializationRoot(cfg), log)
		if err != nil {
			log.Warn("asset transcript: materializer unavailable; ASR fallback disabled (READY text tracks still work)", zap.Error(err))
		} else {
			materializer = m
		}
	}

	transcriptResolver := clipadapters.NewClipRenderTranscriptResolver(log)
	transcriptResolver.SetRepo(root.Repos.TextTrackRepo)
	if root.TextTracks != nil {
		transcriptResolver.SetAcquire(root.TextTracks.AcquireService)
	}
	if root.Domains != nil {
		transcriptResolver.SetCueWriter(root.Domains.CueWriter)
	}
	if streaming, err := clipadapters.NewClipRenderStreamingTranscriber(cfg, log); err != nil {
		log.Warn("asset transcript: streaming Whisper unavailable; using the acquisition fallback", zap.Error(err))
	} else {
		streaming.SetArtifactCache(clipRenderArtifactCache(cfg, root, log), whisperBridgeVersion("scripts/bridges/whisper_transcriber.py"))
		transcriptResolver.SetStreaming(streaming)
	}

	resolver, err := clipadapters.NewAssetTranscriptResolver(assetResolver, materializer, transcriptResolver, root.Repos.TextTrackRepo, log)
	if err != nil {
		log.Warn("asset transcript: resolver not wired; asset route will answer 503", zap.Error(err))
		return nil
	}
	log.Info("asset transcript: wired (READY text track + streaming ASR fallback)")
	return resolver
}
