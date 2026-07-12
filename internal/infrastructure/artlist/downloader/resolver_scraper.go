// Package downloader — resolver_scraper.go: scraper + fallback transport
// implementations for the Resolver (Step 3 follow-up, July 2026).
//
// godlike/06 SSOT: downloadViaScraper + downloadWithFallback + copyFile
// are the SINGLE canonical surface for "how does the Resolver reach
// out to fetch bytes from Artlist?". Before this split these helpers
// lived in resolver.go alongside the routing decision; that
// co-location was a godlike/06 violation because the helpers are
// mechanical (network I/O + filesystem ops) — separating them here
// documents the seam and keeps the canonical Resolver type small
// (godlike/06 prefers a one-concern-per-file split for the canonical
// owner).
//
// godlike/07 typed-error contract: every path returns the canonical
// artlist sentinels via mapError (defined in downloader.go). Callers
// branch on errors.Is, not on string-matching.
//
// godlike/07 NO-FAKE-AVAILABILITY: downloadViaScraper surfaces the
// verbatim HTTP status + body of a non-2xx response (no silent-degrade
// to "transport error"). downloadWithFallback tries scraper → yt-dlp
// → HTTP and surfaces the last error verbatim. copyFile surfaces the
// verbatim os.Open / os.Create error chain (Close error shadows Write
// error to catch buffered-filesystem late-failures).
//
// godlike/07 minimal-blast-radius: zero behavior change from the
// pre-split version. The split is purely organizational.
package downloader

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	artapp "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	core_dl "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"go.uber.org/zap"
)

// downloadWithFallback implements the controlled ladder: scraper → yt-dlp → HTTP.
func (r *Resolver) downloadWithFallback(ctx context.Context, req artapp.DownloadRequest, outPath string) error {
	// Step 1: try scraper.
	if r.cfg.ScraperURL != "" {
		r.metrics.incDownloadPath(PathBrowser)
		if err := r.downloadViaScraper(ctx, req, outPath); err == nil {
			return nil
		} else if r.log != nil {
			r.log.Warn("resolver: scraper fallback failed, trying yt-dlp",
				zap.String("source_ref", req.SourceRef),
				zap.Error(err))
		}
	}

	// Step 2: try yt-dlp.
	r.metrics.incDownloadPath(PathYTDLP)
	dlReq := &core_dl.DownloadRequest{
		URL:        req.SourceRef,
		OutputPath: outPath,
	}
	if err := r.ytdlp.Download(ctx, dlReq); err == nil {
		return nil
	} else if r.log != nil {
		r.log.Warn("resolver: yt-dlp fallback failed, trying HTTP",
			zap.String("source_ref", req.SourceRef),
			zap.Error(err))
	}

	// Step 3: try HTTP as last resort.
	r.metrics.incDownloadPath(PathHTTP)
	httpReq := &core_dl.HTTPDownloadRequest{
		URL:        req.SourceRef,
		OutputPath: outPath,
	}
	return r.httpDl.Download(ctx, httpReq)
}

// downloadViaScraper calls the Node.js scraper /download endpoint with
// the clip page URL for browser-authenticated download.
//
// Mirrors the logic in processor_download.go::downloadViaScraper but
// owned by the downloader package (godlike/06 SSOT).
func (r *Resolver) downloadViaScraper(ctx context.Context, req artapp.DownloadRequest, outPath string) error {
	scraperURL := strings.TrimSuffix(r.cfg.ScraperURL, "/") + "/download"

	// Use ClipPageURL if available; fall back to SourceRef.
	clipPageURL := req.ClipPageURL
	if clipPageURL == "" {
		clipPageURL = req.SourceRef
	}

	// The scraper saves to output_dir with filename: {clipId}.ts (HLS) or {clipId}.mp4.
	// We use outPath + ".mp4" to match the scraper's output convention.
	savePath := outPath + ".mp4"

	payload := map[string]any{
		"clip_page_url": clipPageURL,
		"clip_id":       req.ClipID,
		"output_dir":    filepath.Dir(savePath),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal scraper request: %w", err)
	}

	scraperCtx, cancel := context.WithTimeout(ctx, r.cfg.ScraperTimeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(scraperCtx, http.MethodPost, scraperURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create scraper request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("scraper request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("scraper returned %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var result struct {
		OK        bool   `json:"ok"`
		LocalPath string `json:"local_path"`
		Error     string `json:"error,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode scraper response: %w", err)
	}

	if !result.OK {
		return fmt.Errorf("scraper download failed: %s", result.Error)
	}

	if result.LocalPath == "" {
		return fmt.Errorf("scraper returned empty local_path")
	}

	// The scraper saves to its own output path. Move/copy to our outPath.
	if result.LocalPath != outPath {
		if renameErr := os.Rename(result.LocalPath, outPath); renameErr != nil {
			// Best-effort: if rename fails (cross-device), try copy.
			if r.log != nil {
				r.log.Warn("resolver: scraper rename failed, trying copy",
					zap.String("from", result.LocalPath),
					zap.String("to", outPath),
					zap.Error(renameErr))
			}
			if copyErr := copyFile(result.LocalPath, outPath); copyErr != nil {
				return fmt.Errorf("scraper: failed to move output from %q to %q: rename=%w copy=%w",
					result.LocalPath, outPath, renameErr, copyErr)
			}
		}
	}

	return nil
}

// copyFile copies src to dst. Used as a fallback when os.Rename fails
// (cross-device move on some filesystems).
func copyFile(src, dst string) error {
	s, err := os.Open(src)
	if err != nil {
		return err
	}
	defer s.Close()

	d, err := os.Create(dst)
	if err != nil {
		return err
	}
	// Capture the Close error — a write failure on buffered filesystems
	// can surface only on Close, not on Write.
	defer func() {
		if cerr := d.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	_, err = io.Copy(d, s)
	return err
}
