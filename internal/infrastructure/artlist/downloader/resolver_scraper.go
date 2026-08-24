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

	artapp "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/artlist"
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

	// Strict path-traversal guard (P0-1 fix): the scraper advertises a
	// local filesystem path that we then rename/copy into outPath. We
	// MUST verify that result.LocalPath lives inside the configured
	// scraper output directory (req.DestinationID). Anything outside
	// is rejected as ErrInvalidResponse so a malicious or buggy
	// scraper response cannot redirect our copy into a privileged file.
	//
	//  - filepath.IsAbs: defend against relative responses that
	//    filepath.Clean would interpret relative to our CWD.
	//  - filepath.Rel: clean -> relative-path member test. Windows
	//    different-drive relErr is also caught. rel == ".." catches
	//    the exact-parent edge case.
	cleanRoot := filepath.Clean(req.DestinationID)
	cleanCand := filepath.Clean(result.LocalPath)
	if !filepath.IsAbs(cleanCand) {
		return fmt.Errorf("%w: scraper returned non-absolute local_path %q (must be absolute under output_dir %q)", artapp.ErrInvalidResponse, result.LocalPath, cleanRoot)
	}
	rel, relErr := filepath.Rel(cleanRoot, cleanCand)
	if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%w: scraper returned local_path %q outside of configured output directory %q", artapp.ErrInvalidResponse, result.LocalPath, cleanRoot)
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
//
// Uses a NAMED return value (retErr) so the deferred Close-capture can
// surface write/flush errors that otherwise appear only at Close time on
// buffered filesystems. The pre-fix signature used a plain `err`
// parameter; the deferred `err = cerr` mutated the local AFTER the
// naked return had already copied the value, silently dropping the
// Close error (P0-1 bug; see REPORT_ARCH.md verdict).
//
// On any failure path the partial destination is unlinked so callers
// never observe a half-written file masquerading as a real download
// (no-fake-availability contract).
func copyFile(src, dst string) (retErr error) {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	// LIFO order: dstFile closes FIRST (writes/flush errors take
	// priority), then srcFile closes best-effort.
	defer func() {
		if closeErr := dstFile.Close(); closeErr != nil && retErr == nil {
			retErr = closeErr
		}
		if retErr != nil {
			_ = os.Remove(dst)
		}
	}()

	_, retErr = io.Copy(dstFile, srcFile)
	return retErr
}
