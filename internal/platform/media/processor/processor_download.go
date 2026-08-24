package processor

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	artlist_dl "github.com/Marcuss-ops/PipelineGen/internal/platform/artlist/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ytdlp"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// downloadStep downloads the asset from the source URL.
//
// PR-ARTLIST-DOWNLOAD-SURFACE-UNIFY-CUTOVER (July 2026): URL
// classification uses the canonical exported helpers from the
// artlist/downloader package (IsArtlistURL / IsDirectMediaURL /
// IsHLSURL), eliminating the byte-equivalent duplicates
// (p.isArtlistURL / p.isDirectURL / p.isHLSURL) that previously
// lived in this file.
//
// Artlist-clip downloads route through the injected
// p.artlistDL.DownloadArtlistClip() (wired via
// build_bundles_artlist.go wrapping the Resolver).
// When nil, Artlist clips fall through to yt-dlp (Rule 4).
//
// PR-ARTLIST-SCRAPER-RETIRE (July 2026): the legacy
// downloadViaScraper fallback, buildArtlistClipPageURL helper,
// and processor.scraperURL field are RETIRED.
//
// Ladder:
//  1. Direct MP4/MOV/AVI → HTTP download
//  2. Artlist clip (IsArtlistURL / ClipPageURL / numeric ID)
//     → ArtlistDownloader or yt-dlp
//  3. Non-Artlist HLS (.m3u8) → FFmpeg RemuxHLS
//  4. Everything else → yt-dlp
func (p *Processor) downloadStep(ctx context.Context, input *asset.ProcessInput, rawPath string) (actualPath string, err error) {
	// Rule 1: Direct progressive media — HTTP download.
	if p.httpDL != nil && artlist_dl.IsDirectMediaURL(input.SourceURL) {
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

	// Rule 2: Artlist clips — route through the canonical Resolver.
	isArtlistClip := artlist_dl.IsArtlistURL(input.SourceURL) ||
		artlist_dl.IsArtlistURL(input.ClipPageURL) ||
		(input.ID != "" && input.Name != "" && isArtlistNumericID(input.ID))
	if isArtlistClip {
		if p.artlistDL != nil {
			p.log.Info("using ArtlistDownloader (Resolver) for Artlist download",
				zap.String("id", input.ID),
				zap.String("name", input.Name))
			localPath, dlErr := p.artlistDL.DownloadArtlistClip(ctx,
				input.SourceURL, input.ClipPageURL, input.ID,
				filepath.Dir(rawPath), filepath.Base(rawPath))
			if dlErr == nil {
				p.log.Info("ArtlistDownloader succeeded", zap.String("path", localPath))
				return localPath, nil
			}
			p.log.Warn("ArtlistDownloader failed, falling back to yt-dlp",
				zap.String("id", input.ID),
				zap.Error(dlErr))
		}
		// PR-ARTLIST-SCRAPER-RETIRE (July 2026): the legacy
		// downloadViaScraper fallback is RETIRED. When
		// artlistDL is nil, the Artlist clip falls through
		// to yt-dlp (Rule 4). The processor.scraperURL field
		// + buildArtlistClipPageURL helper were also removed.
	}

	// Rule 3: Non-Artlist HLS — FFmpeg RemuxHLS.
	if p.ffmpeg != nil && artlist_dl.IsHLSURL(input.SourceURL) {
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

	// Rule 4: Fallthrough — yt-dlp for everything else.
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

// isArtlistNumericID checks if the clip ID looks like an Artlist numeric identifier.
// Artlist clip IDs are 4-7 digit numbers (e.g. 694565, 760198, 489828).
//
// RETAINED — the downloader.Resolver does not replicate numeric-ID
// detection (it only classifies URLs). The heuristic here gates
// whether downloadStep tries the Artlist path at all.
func isArtlistNumericID(id string) bool {
	id = strings.TrimSpace(id)
	if len(id) < 4 || len(id) > 7 {
		return false
	}
	for _, c := range id {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
