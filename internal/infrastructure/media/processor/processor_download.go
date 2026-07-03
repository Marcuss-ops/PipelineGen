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
	downloader "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
)

// downloadStep downloads the asset from the source URL.
func (p *Processor) downloadStep(ctx context.Context, input *asset.ProcessInput, rawPath string) (actualPath string, err error) {
	// Try HTTP download first for direct URLs (e.g., Artlist with direct links).
	if p.httpDL != nil && p.isDirectURL(input.SourceURL) {
		p.log.Info("using HTTP downloader for direct URL", zap.String("id", input.ID), zap.String("url", input.SourceURL))
		httpReq := &downloader.HTTPDownloadRequest{
			URL:        input.SourceURL,
			OutputPath: rawPath,
		}
		if err := p.httpDL.Download(ctx, httpReq); err != nil {
			p.log.Warn("HTTP download failed, falling back to yt-dlp", zap.Error(err))
			// Fall through to yt-dlp.
		} else {
			p.log.Info("HTTP download succeeded", zap.String("path", rawPath))
			return rawPath, nil
		}
	}

	// For Artlist clips, use the Node.js scraper with browser cookies.
	// Artlist CDN URLs often lack explicit .m3u8 extension but are HLS streams.
	// FFmpeg/yt-dlp fail with 403 because they lack browser session cookies.
	// The scraper (Puppeteer/Chromium) carries the proper auth context, opens
	// the clip page, scrolls into view, clicks play, and captures the stream URL.
	// Trigger when: the clip is from Artlist (identified by SourceURL, ClipPageURL,
	// or by having an Artlist-style numeric ID with a name).
	isArtlistClip := p.isArtlistURL(input.SourceURL) || p.isArtlistURL(input.ClipPageURL) ||
		(input.ID != "" && input.Name != "" && p.isArtlistNumericID(input.ID))
	if p.ffmpeg != nil && isArtlistClip {
		p.log.Info("using Node.js scraper for Artlist download",
			zap.String("id", input.ID),
			zap.String("name", input.Name),
		)

		scraperOutput, scraperErr := p.downloadViaScraper(ctx, input, rawPath)
		if scraperErr == nil {
			p.log.Info("Artlist scraper download succeeded", zap.String("path", scraperOutput))
			return scraperOutput, nil
		}
		p.log.Warn("Artlist scraper download failed, falling back to yt-dlp",
			zap.String("id", input.ID),
			zap.Error(scraperErr),
		)
	}

	// Use FFmpeg for other HLS URLs (non-Artlist, e.g. direct m3u8).
	if p.ffmpeg != nil && p.isHLSURL(input.SourceURL) {
		p.log.Info("using FFmpeg for HLS URL",
			zap.String("id", input.ID),
			zap.String("url", input.SourceURL),
		)

		hlsOutputPath := rawPath + ".mp4"
		if err := p.ffmpeg.RemuxHLS(ctx, input.SourceURL, hlsOutputPath); err != nil {
			p.log.Warn("FFmpeg HLS remux failed, falling back to yt-dlp", zap.Error(err))
		} else {
			p.log.Info("FFmpeg HLS remux succeeded", zap.String("path", hlsOutputPath))
			return hlsOutputPath, nil
		}
	}

	// Use yt-dlp for complex URLs (YouTube, etc.).
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

// isDirectURL checks if URL is likely a direct download (not needing yt-dlp).
func (p *Processor) isDirectURL(url string) bool {
	// Check for known direct download patterns.
	directPatterns := []string{
		"artlist.io/download",
		"artlist.io/api",
		".mp4",
		".mov",
		".avi",
	}
	for _, pattern := range directPatterns {
		if strings.Contains(url, pattern) {
			return true
		}
	}
	return false
}

// isHLSURL checks if URL points to an HLS playlist.
func (p *Processor) isHLSURL(url string) bool {
	u := strings.ToLower(strings.TrimSpace(url))
	return strings.Contains(u, ".m3u8")
}

// isArtlistURL checks if URL is from Artlist (needs browser cookies to download).
func (p *Processor) isArtlistURL(url string) bool {
	u := strings.ToLower(strings.TrimSpace(url))
	return strings.Contains(u, "artlist") || strings.Contains(u, "cdn.artlist")
}

// isArtlistNumericID checks if the clip ID looks like an Artlist numeric identifier.
// Artlist clip IDs are 4-7 digit numbers (e.g. 694565, 760198, 489828).
func (p *Processor) isArtlistNumericID(id string) bool {
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

// buildArtlistClipPageURL constructs an Artlist clip page URL from the clip name and ID.
// Artlist URLs follow the pattern: https://artlist.io/stock-footage/clip/<slug>/<id>
// where <slug> is a URL-safe version of the clip name.
func buildArtlistClipPageURL(name, id string) string {
	if id == "" {
		return ""
	}
	// Derive a slug from the clip name: lowercase, remove non-alphanumeric
	// chars, collapse spaces to hyphens, trim.
	slug := strings.ToLower(name)
	// Remove everything after " by " (the author attribution)
	if idx := strings.Index(slug, " by "); idx >= 0 {
		slug = slug[:idx]
	}
	// Replace common separators and spaces with hyphens
	slug = strings.ReplaceAll(slug, ",", "")
	slug = strings.ReplaceAll(slug, "  ", " ")
	slug = strings.TrimSpace(slug)
	slug = strings.ReplaceAll(slug, " ", "-")
	// Remove any remaining non-alpha-numeric-hyphen characters
	var cleaned strings.Builder
	for _, c := range slug {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			cleaned.WriteRune(c)
		}
	}
	slug = cleaned.String()
	// Remove leading/trailing hyphens and collapse multiple hyphens
	slug = strings.Trim(slug, "-")
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	if slug == "" {
		return ""
	}
	return fmt.Sprintf("https://artlist.io/stock-footage/clip/%s/%s", slug, id)
}

// downloadViaScraper calls the Node.js scraper /download endpoint to download
// an Artlist clip with browser authentication (cookies).
func (p *Processor) downloadViaScraper(ctx context.Context, input *asset.ProcessInput, rawPath string) (string, error) {
	scraperURL := strings.TrimSuffix(p.scraperURL, "/") + "/download"

	// Use ClipPageURL if available (the actual Artlist clip page URL for browser navigation).
	// Fall back to SourceURL (which might be the .m3u8 URL).
	// If both are empty, construct the URL from clip name + ID.
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

	// The scraper saves to output_dir with filename: {clipId}.ts (for HLS) or {clipId}.mp4.
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
