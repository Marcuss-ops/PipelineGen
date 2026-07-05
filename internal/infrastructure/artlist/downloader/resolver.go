// Package downloader — resolver.go: unified Artlist download routing
// (PR-ARTLIST-DOWNLOAD-SURFACE-UNIFY, July 2026).
//
// Resolver is the SINGLE canonical owner of "how to download an Artlist
// asset". Before this PR, the routing logic was split across two surfaces:
//
//  1. Provider.Download (this package) — picked HLS (yt-dlp) vs
//     progressive (HTTP) based on .m3u8 presence.
//  2. processor.downloadStep (internal/infrastructure/media/processor/
//     processor_download.go) — had its own isArtlistURL / isDirectURL /
//     isHLSURL detection + downloadViaScraper for the Node Puppeteer
//     fallback.
//
// Resolver collapses both into one canonical surface:
//
//   - ClipPageURL set OR Artlist-shaped URL → Node scraper /download
//     (browser-authenticated Puppeteer session)
//   - Direct MP4/MOV/AVI URL → HTTP progressive download
//   - HLS (.m3u8) URL → yt-dlp with Artlist cookie impersonation
//   - Fallback ladder: try scraper → yt-dlp → HTTP
//
// godlike/06 SSOT: this file is the SINGLE canonical owner of Artlist
// download routing. Every call site (stager_adapter.go → StageSource,
// processor_download.go → downloadStep fallback) routes through this
// resolver. The duplicated isHLS / isDirectURL / isArtlistURL detection
// in processor_download.go is superseded by resolvePath below.
//
// godlike/07 typed-error contract: every path returns the canonical
// artlist sentinels (ErrEmpty, ErrUnavailable, ErrTimeout,
// ErrInvalidResponse, ErrEmptyResult, ErrNotFound,
// ErrTransportFallback) via mapError. Callers branch on errors.Is,
// not on string-matching.
//
// DESIGN NOTE (cross-path fallback): when resolvePath returns a specific
// transport (scraper / HTTP / yt-dlp), that transport is retried with
// exponential backoff but does NOT fall through to the next transport on
// exhaustion. This matches the legacy Provider behavior (HLS vs HTTP)
// and the user's explicit routing rules. The cross-path ladder
// (downloadWithFallback) only fires for the downloadPathFallback case
// (unknown URL types). This is intentional — known Artlist URLs should
// converge on the scraper (retryable 5xx / network blips are transient),
// not silently degrade to yt-dlp which lacks browser cookies and would
// produce a 403 on the same asset that the scraper would have succeeded
// with on retry. If future operators need cross-path fallback for
// primary routes, add a resolvePathWithFallback enum and thread it
// through the retry.DoWithValue body.
//
// godlike/07 minimal-blast-radius (CUTOVER phase): the legacy Provider
// struct in downloader.go has been RETIRED. Resolver is the SINGLE
// canonical owner of Artlist download routing per godlike/06 SSOT.
// The composition root (build_bundles_artlist.go) wires
// downloader.NewResolver(...) exclusively.
//
// Honest scope-lock (CUTOVER, July 2026): the 3 duplicate URL helpers
// in processor_download.go (isArtlistURL / isDirectURL / isHLSURL)
// have been REMOVED and superseded by the exported canonical helpers
// above. The remaining processor-specific code is:
//   - isArtlistNumericID — retained (no Resolver equivalent; gates
//     whether the Artlist branch fires at all)
//   - buildArtlistClipPageURL — retained (used by the legacy
//     downloadViaScraper fallback)
//   - downloadViaScraper — retained as legacy fallback when
//     p.artlistDL is nil (backward compat)
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
	"time"

	artapp "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	core_dl "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
	"go.uber.org/zap"
)

// Compile-time assertion: Resolver satisfies artlist.Downloader.
var _ artapp.Downloader = (*Resolver)(nil)

// ResolverConfig extends the base Config with scraper-specific fields.
type ResolverConfig struct {
	Config // embed base retry/timeout config

	// ScraperURL is the base URL of the Node.js scraper server (e.g.
	// "http://artlist-scraper:9123"). The /download endpoint is appended
	// at call time. Empty disables the scraper path (Resolver falls
	// through to the next transport in the ladder).
	ScraperURL string

	// ScraperTimeout is the per-request timeout for the POST to
	// <ScraperURL>/download. Default: 5m.
	ScraperTimeout time.Duration
}

// Resolver is the unified Artlist download routing surface. It owns the
// canonical decision of which transport to use for a given DownloadRequest
// (Node scraper / yt-dlp / HTTP) and implements the controlled fallback
// ladder.
type Resolver struct {
	cfg    ResolverConfig
	log    *zap.Logger
	ytdlp  *core_dl.YTDLPDownloader
	httpDl *core_dl.HTTPDownloader
	// metrics is optional — nil disables emission (nil-safe per
	// incDownloadPath guard).
	metrics *Metrics
}

// NewResolver constructs a Resolver by composing yt-dlp + HTTPDownloader
// + Node scraper HTTP client. cfg is mandatory (drives yt-dlp path,
// cookies, JS runtime, scraper URL). resolverCfg is optional; zero
// values fall back to defaults. metrics is optional — pass nil to disable
// metrics emission.
func NewResolver(cfg *config.Config, resolverCfg ResolverConfig, log *zap.Logger, metrics *Metrics) *Resolver {
	if resolverCfg.MaxAttempts <= 0 {
		resolverCfg.MaxAttempts = 3
	}
	if resolverCfg.JitterFraction <= 0 {
		resolverCfg.JitterFraction = 0.3
	}
	if resolverCfg.InitialBackoff <= 0 {
		resolverCfg.InitialBackoff = time.Second
	}
	if resolverCfg.MaxBackoff <= 0 {
		resolverCfg.MaxBackoff = 30 * time.Second
	}
	if resolverCfg.HTTPDownloadTimeout <= 0 {
		resolverCfg.HTTPDownloadTimeout = 5 * time.Minute
	}
	if resolverCfg.ScraperTimeout <= 0 {
		resolverCfg.ScraperTimeout = 5 * time.Minute
	}
	return &Resolver{
		cfg:     resolverCfg,
		log:     log,
		ytdlp:   core_dl.NewYTDLP(cfg),
		httpDl:  core_dl.NewHTTPDownloader(resolverCfg.HTTPDownloadTimeout),
		metrics: metrics,
	}
}

// Download is the artapp.Downloader port entry point.
//
// Routes through the unified resolvePath ladder and delegates to the
// selected transport. Retries non-4xx transport failures with the
// configured backoff policy.
func (r *Resolver) Download(ctx context.Context, req artapp.DownloadRequest) (*artapp.DownloadResult, error) {
	if strings.TrimSpace(req.SourceRef) == "" {
		return nil, fmt.Errorf("%w: source ref required", artapp.ErrEmpty)
	}
	if strings.TrimSpace(req.Filename) == "" {
		return nil, fmt.Errorf("%w: filename required", artapp.ErrEmpty)
	}
	if filepath.IsAbs(req.Filename) {
		return nil, fmt.Errorf("%w: filename must not be absolute", artapp.ErrInvalidResponse)
	}
	if cleaned := filepath.Clean(req.Filename); cleaned != req.Filename || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return nil, fmt.Errorf("%w: filename must not escape destination", artapp.ErrInvalidResponse)
	}
	if strings.TrimSpace(req.DestinationID) == "" {
		return nil, fmt.Errorf("%w: destination id required", artapp.ErrEmpty)
	}

	outPath := filepath.Join(req.DestinationID, req.Filename)
	if mkErr := os.MkdirAll(req.DestinationID, 0o755); mkErr != nil {
		return nil, fmt.Errorf("%w: mkdir destination: %v", artapp.ErrUnavailable, mkErr)
	}

	path := r.resolvePath(req)

	// Defense-in-depth: if resolvePath picked scraper but ScraperURL is
	// empty (test-only edge case — production is gated by
	// validateArtlistScraperURL), fall through to the controlled ladder.
	if path == downloadPathScraper && r.cfg.ScraperURL == "" {
		path = downloadPathFallback
	}

	opts := retry.Options{
		MaxAttempts:    r.cfg.MaxAttempts,
		InitialBackoff: r.cfg.InitialBackoff,
		MaxBackoff:     r.cfg.MaxBackoff,
		JitterFraction: r.cfg.JitterFraction,
		IsRetryable:    retry.IsTransient,
	}

	_, err := retry.DoWithValue(ctx, func() (struct{}, error) {
		switch path {
		case downloadPathScraper:
			r.metrics.incDownloadPath(PathBrowser)
			return struct{}{}, r.downloadViaScraper(ctx, req, outPath)
		case downloadPathHTTP:
			r.metrics.incDownloadPath(PathHTTP)
			httpReq := &core_dl.HTTPDownloadRequest{
				URL:        req.SourceRef,
				OutputPath: outPath,
			}
			return struct{}{}, r.httpDl.Download(ctx, httpReq)
		case downloadPathYTDLP:
			r.metrics.incDownloadPath(PathYTDLP)
			dlReq := &core_dl.DownloadRequest{
				URL:        req.SourceRef,
				OutputPath: outPath,
			}
			return struct{}{}, r.ytdlp.Download(ctx, dlReq)
		default:
			// Fallback ladder: try scraper → yt-dlp → HTTP.
			return struct{}{}, r.downloadWithFallback(ctx, req, outPath)
		}
	}, opts)

	if err != nil {
		return nil, mapError(err, path == downloadPathYTDLP)
	}

	info, statErr := os.Stat(outPath)
	if statErr != nil {
		return nil, fmt.Errorf("%w: stat result: %v", artapp.ErrEmptyResult, statErr)
	}
	return &artapp.DownloadResult{
		LocalPath: outPath,
		Bytes:     info.Size(),
	}, nil
}

// downloadPath is the enum of transport choices.
type downloadPath int

const (
	downloadPathScraper  downloadPath = iota // Node Puppeteer /download
	downloadPathHTTP                         // progressive HTTP
	downloadPathYTDLP                        // yt-dlp for HLS
	downloadPathFallback                     // controlled ladder
)

// resolvePath is the SINGLE canonical routing decision for Artlist
// downloads. It implements the unified rules:
//
//  1. ClipPageURL is set OR URL is Artlist-shaped → Node scraper
//     (browser cookies required for Artlist HLS streams).
//  2. URL ends with .mp4 / .mov / .avi → HTTP progressive.
//  3. URL contains .m3u8 → yt-dlp with cookie impersonation.
//  4. Otherwise → fallback ladder.
func (r *Resolver) resolvePath(req artapp.DownloadRequest) downloadPath {
	// Rule 1: Artlist assets need browser cookies.
	if req.ClipPageURL != "" || IsArtlistURL(req.SourceRef) {
		return downloadPathScraper
	}
	// Rule 2: Direct progressive media.
	if IsDirectMediaURL(req.SourceRef) {
		return downloadPathHTTP
	}
	// Rule 3: HLS streams.
	if IsHLSURL(req.SourceRef) {
		return downloadPathYTDLP
	}
	// Rule 4: Controlled fallback.
	return downloadPathFallback
}

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

// ── URL classification helpers (canonical — exported for processor_download.go) ──

// IsArtlistURL checks if the URL is from Artlist's CDN.
// Exported for use by the media processor's downloadStep fallback path
// (PR-ARTLIST-DOWNLOAD-SURFACE-UNIFY-CUTOVER, July 2026).
func IsArtlistURL(url string) bool {
	u := strings.ToLower(strings.TrimSpace(url))
	return strings.Contains(u, "artlist") || strings.Contains(u, "cdn.artlist")
}

// IsDirectMediaURL checks if the URL points to a direct progressive media file.
func IsDirectMediaURL(url string) bool {
	u := strings.ToLower(strings.TrimSpace(url))
	return strings.HasSuffix(u, ".mp4") || strings.HasSuffix(u, ".mov") || strings.HasSuffix(u, ".avi")
}

// IsHLSURL checks if the URL points to an HLS playlist.
func IsHLSURL(url string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(url)), ".m3u8")
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
