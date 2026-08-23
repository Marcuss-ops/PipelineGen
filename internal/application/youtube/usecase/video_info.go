// video_info.go — Drive video metadata + video pipeline cut facade +
// runtime-config accessor.
//
// Split out of orchestrator.go in Step 4 so each usecase/ file owns exactly
// one responsibility per topic cluster. Config() lives here because it is
// a generic accessor that goes naturally with the read-side metadata
// helpers; moving it to service.go would mix struct definition with method
// bodies.
package usecase

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockplan"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
)

// GetVideoInfo fetches full YouTube metadata for the given URL by
// forwarding to the canonical VideoMetadataFetcherPort (which exposes
// GetVideoMetadata(ctx, videoURL) with the same return type).
//
// Fulfils the extraction.ExtractionCallbacks.GetVideoInfo interface
// requirement on *Service. The isUnavailablePort guard mirrors sibling
// forwarding methods so a nil-typed metaFetcher surfaces an explicit
// error instead of a nil-deref panic.
func (s *Service) GetVideoInfo(ctx context.Context, url string) (*youtubeports.DownloaderMetadata, error) {
	if isUnavailablePort(s.metaFetcher) {
		return nil, fmt.Errorf("youtube: metaFetcher port not wired")
	}
	return s.metaFetcher.GetVideoMetadata(ctx, url)
}

// DownloadAndCut delegates to the VideoPipeline port. The orchestrator no
// longer calls a concrete videomuscles.Pipeline application-side; the
// pipeline adapter is composed by BuildDomainBundle (build_bundles_domain.go).
func (s *Service) DownloadAndCut(ctx context.Context, req youtubeports.VideoCutRequest) (*youtubeports.VideoCutResult, error) {
	if isUnavailablePort(s.videoPipeline) {
		return nil, fmt.Errorf("youtube: video pipeline not wired")
	}
	return s.videoPipeline.DownloadAndCutYouTubeVideo(ctx, req)
}

// AcquireStockTranscript delegates transcript acquisition to the canonical
// TextTrackResolver used by per-segment extraction. The stock planner calls
// this before any video section download.
func (s *Service) AcquireStockTranscript(ctx context.Context, videoID string, durationMs int64) (*stockplan.Transcript, error) {
	if s == nil || s.processSeg == nil {
		return nil, fmt.Errorf("youtube: process segment use case not wired")
	}
	bundle, err := s.processSeg.AcquireStockTranscript(ctx, videoID, durationMs)
	if err != nil {
		return nil, err
	}
	return stockplan.TranscriptFromBundle(bundle), nil
}

// Config returns the resolved runtime configuration. The accessor lets
// callers read it without taking a direct dependency on the config
// loader.
func (s *Service) Config() youtubetypes.RuntimeConfig {
	return s.cfg
}
