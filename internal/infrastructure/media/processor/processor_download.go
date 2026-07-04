package processor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	artlist_dl "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/artlist/downloader"
	downloader "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
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
// p.artlistDL.DownloadArtlistClip() when non-nil (wired via
// build_bundles_artlist.go wrapping the Resolver). When nil,
// the legacy downloadViaScraper method handles Artlist downloads.
//
// Ladder:
//   1. Direct MP4/MOV/AVI → HTTP download
//   2. Artlist clip (IsArtlistURL / ClipPageURL / numeric ID)
//      → ArtlistDownloader or legacy downloadViaScraper
//   3. Non-Artlist HLS (.m3u8) → FFmpeg RemuxHLS
//   4. Everything else → yt-dlp
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
		} else if p.ffmpeg != nil {
			// Legacy fallback: Processor's own scraper download.
			p.log.Info("using legacy downloadViaScraper for Artlist download",
				zap.String("id", input.ID),
				zap.String("name", input.Name))
			scraperOutput, scraperErr := p.downloadViaScraper(ctx, input, rawPath)
			if scraperErr == nil {
				p.log.Info("Artlist scraper download succeeded", zap.String("path", scraperOutput))
				return scraperOutput, nil
			}
			p.log.Warn("Artlist scraper download failed, falling back to yt-dlp",
				zap.String("id", input.ID),
				zap.Error(scraperErr))
		}
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
		dlReq.Format = "bv*[height<=1080][ext=mp4]+ba[ext=m4a]/b[height<=1080][ext=mp4]/best[height<=1080]"
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

// buildArtlistClipPageURL constructs an Artlist clip page URL from name + ID.
// RETAINED — used by the legacy downloadViaScraper fallback path below.
// DEPRECATO: remove when downloadViaScraper is retired.
func buildArtlistClipPageURL(name, id string) string {
	if id == "" {
		return ""
	}
	slug := strings.ToLower(name)
	if idx := strings.Index(slug, " by "); idx >= 0 {
		slug = slug[:idx]
	}
	slug = strings.ReplaceAll(slug, ",", "")
	slug = strings.ReplaceAll(slug, "  ", " ")
	slug = strings.TrimSpace(slug)
	slug = strings.ReplaceAll(slug, " ", "-")
	var cleaned strings.Builder
	for _, c := range slug {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			cleaned.WriteRune(c)
		}
	}
	slug = cleaned.String()
	slug = strings.Trim(slug, "-")
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	if slug == "" {
		return ""
	}
	return fmt.Sprintf("https://artlist.io/stock-footage/clip/%s/%s", slug, id)
}

// downloadViaScraper calls the Node.js scraper /download endpoint.
//
// DEPRECATO (PR-ARTLIST-DOWNLOAD-SURFACE-UNIFY-CUTOVER, July 2026):
// when p.artlistDL is wired, downloadStep routes Artlist downloads
// through the canonical downloader.Resolver. This method remains as
// the legacy fallback for callers that haven't wired the
// ArtlistDownloader port. Remove when all composition roots wire
// the Resolver adapter.
func (p *Processor) downloadViaScraper(ctx context.Context, input *asset.ProcessInput, rawPath string) (string, error) {
	scraperURL := strings.TrimSuffix(p.scraperURL, "/") + "/download"

	clipPageURL := input.ClipPageURL
	if clipPageURL == "" {
		clipPageURL = input.SourceURL
	}
	if clipPageURL == "" && input.ID != "" {
		clipPageURL = buildArtlistClipPageURL(input.Name, input.ID)
		p.log.Info("constructed Artlist clip page URL from name+ID",
			zap.String("url", clipPageURL),
			zap.String("name", input.Name),
			zap.String("id", input.ID),
		)
	}

	savePath := rawPath + ".mp4"

	payload := map[string]any{
		"clip_page_url": clipPageURL,
		"clip_id":       input.ID,
		"output_dir":    filepath.Dir(savePath),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal scraper request: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, scraperURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create scraper request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("scraper request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("scraper returned %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var result struct {
		OK        bool   `json:"ok"`
		LocalPath string `json:"local_path"`
		Error     string `json:"error,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode scraper response: %w", err)
	}

	if !result.OK {
		return "", fmt.Errorf("scraper download failed: %s", result.Error)
	}

	if result.LocalPath == "" {
		return "", fmt.Errorf("scraper returned empty local_path")
	}

	return result.LocalPath, nil
}
