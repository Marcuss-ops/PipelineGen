package processor

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ytdlp"
)

// downloadStep downloads the asset from the source URL.
//
// URL classification uses the canonical exported helpers from
// internal/platform/downloader (IsDirectMediaURL / IsHLSURL). They are pure
// shape predicates, so they belong next to the generic downloaders rather than
// to any one provider.
//
// ARTIST-DEMOLITION (September 2026): the Artlist-clip branch (the injected
// ArtlistDownloader + the IsArtlistURL / numeric-ID routing) is RETIRED with the
// Artlist capability. A source that is neither a direct media file nor an HLS
// playlist now always falls through to yt-dlp, which is what the Artlist clips
// already did whenever the injected resolver was absent.
//
// Ladder:
//  1. Direct MP4/MOV/AVI → HTTP download
//  2. HLS (.m3u8)        → FFmpeg RemuxHLS
//  3. Everything else    → yt-dlp
func (p *Processor) downloadStep(ctx context.Context, input *detail.ProcessInput, rawPath string) (actualPath string, err error) {
	// Rule 1: Direct progressive media — HTTP download.
	if p.httpDL != nil && downloader.IsDirectMediaURL(input.SourceURL) {
		p.log.Info("using HTTP downloader for direct URL", zap.String("id", input.ID), zap.String("url", input.SourceURL))
		httpReq := &downloader.HTTPDownloadRequest{
			URL:        input.SourceURL,
			OutputPath: rawPath,
		}
		if err := p.httpDL.Download(ctx, httpReq); err != nil {
			p.log.Warn("HTTP download failed, falling back to yt-dlp", zap.Error(err))
		} else {
			p.log.Info("HTTP download succeeded", zap.String("path", rawPath))
			return rawPath, nil
		}
	}

	// Rule 2: HLS — FFmpeg RemuxHLS.
	if p.ffmpeg != nil && downloader.IsHLSURL(input.SourceURL) {
		p.log.Info("using FFmpeg for HLS URL",
			zap.String("id", input.ID),
			zap.String("url", input.SourceURL))

		hlsOutputPath := rawPath + ".mp4"
		if err := p.ffmpeg.RemuxHLS(ctx, input.SourceURL, hlsOutputPath); err != nil {
			p.log.Warn("FFmpeg HLS remux failed, falling back to yt-dlp", zap.Error(err))
		} else {
			p.log.Info("FFmpeg HLS remux succeeded", zap.String("path", hlsOutputPath))
			return hlsOutputPath, nil
		}
	}

	// Rule 3: Fallthrough — yt-dlp for everything else.
	dlReq := &downloader.DownloadRequest{
		URL:              input.SourceURL,
		OutputPath:       rawPath,
		ForceKeyframes:   input.ForceKeyframes,
		DownloadSections: input.DownloadSections,
		StreamCopy:       input.StreamCopy,
	}
	if len(input.DownloadSections) > 0 {
		// Section downloads use the bounded selector; full-source downloads
		// retain the canonical 1080p selector.
		dlReq.Format = ytdlp.DefaultYouTubeSectionFormatSelectors
		dlReq.MergeFormat = "mp4"
		dlReq.NoPlaylist = true
		dlReq.Timeout = 10 * time.Minute
	}

	p.log.Info("downloading asset with yt-dlp", zap.String("id", input.ID), zap.String("url", input.SourceURL), zap.Strings("sections", input.DownloadSections))
	if err := p.dl.Download(ctx, dlReq); err != nil {
		return "", err
	}

	actualPath = ResolveDownloadedFile(rawPath)
	if actualPath != rawPath {
		p.log.Info("resolved actual download path", zap.String("expected", rawPath), zap.String("actual", actualPath))
	}

	return actualPath, nil
}
