// Package downloader — resolver.go: unified Artlist download routing
// (PR-ARTLIST-DOWNLOAD-SURFACE-UNIFY, July 2026).
//
// Resolver is the SINGLE canonical owner of "how to download an Artlist
// asset" (godlike/06 SSOT). The 580-LOC file was split into 3 seams in
// Step 3 follow-up (July 2026):
//
//   - resolver.go (CANONICAL, this file): the Resolver concrete type,
//     the Download entry point, the resolvePath routing decision, the
//     downloadPath enum, the ResolverConfig, the compile-time pin
//     `var _ artapp.Downloader = (*Resolver)(nil)`. godlike/06 SSOT:
//     the canonical owner of routing decisions + the Pattern 0 compile-
//     time surface for the artapp.Downloader port.
//   - resolver_url_helpers.go: pure URL classification helpers
//     (IsArtlistURL, IsDirectMediaURL, IsHLSURL) + firstNonEmpty.
//     No state, no I/O; callable from any package (notably
//     internal/infrastructure/media/processor/processor_download.go uses
//     the URL helpers in its own routing decision).
//   - resolver_scraper.go: downloadViaScraper + downloadWithFallback
//   - copyFile. Network I/O + filesystem ops live here.
//
// godlike/07 typed-error contract: every path returns the canonical
// artlist sentinels (ErrEmpty, ErrUnavailable, ErrTimeout,
// ErrInvalidResponse, ErrEmptyResult, ErrNotFound,
// ErrTransportFallback) via mapError. Callers branch on errors.Is,
// not on string-matching.
//
// godlike/07 minimal-blast-radius: the Step 3 split is purely
// organizational — zero production logic changes. All 3 files are
// in `package downloader` so cross-file symbol resolution is
// package-internal (no new exports, no API surface change).
package downloader

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	artapp "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/artlist"
	coredl "github.com/Marcuss-ops/PipelineGen/internal/platform/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
	"go.uber.org/zap"
)

// Compile-time assertion: Resolver satisfies artlist.Downloader.
// Pattern 0 SSOT pin (godlike/06) — drift in the artapp.Downloader port
// signature surfaces as a build failure here, not a runtime panic on
// first dispatch. Lives in the canonical file (resolver.go) per
// godlike/06 SSOT — the pin is the canonical ownership signal.
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

	// AcquisitionMode controls whether automatic downloads are allowed.
	// manual_import blocks all downloads; authorized_api enforces the
	// daily limit and records an audit row for every fetch.
	AcquisitionMode artapp.ArtlistAcquisitionMode

	// AccountID identifies the account for rate-limit and audit tracking.
	AccountID string

	// DailyDownloadLimit is the maximum number of automatic downloads
	// allowed per account per day. 0 disables automatic downloads.
	DailyDownloadLimit int

	// AuditRepository persists download events and answers daily-count
	// queries. Required when AcquisitionMode is authorized_api.
	AuditRepository artapp.DownloadAuditRepository

	// PostValidator runs AFTER the transport completes AND AFTER
	// os.Stat, but BEFORE the audit row flips to succeeded.
	//
	// PR-HLS-AES128 (P1, July 2026): the production composition wires
	// this to the canonical ffmpeg.Processor.Probe(ctx, path) gate so
	// every authorized_api download is ffprobe-validated + stat-positive
	// BEFORE markAudit(Succeeded) can fire
	// (godlike/07 no-fake-availability).
	// If the validator returns a non-nil error the audit row is
	// flipped to Failed and the typed error surfaces to the caller.
	//
	// If nil, no PostValidator runs (defense-in-depth for non-HLS
	// paths in tests or deployments where ffprobe is intentionally
	// omitted). This keeps the existing test surface backward-compat.
	PostValidator func(ctx context.Context, path string) error
}

// Resolver is the unified Artlist download routing surface. It owns the
// canonical decision of which transport to use for a given DownloadRequest
// (Node scraper / yt-dlp / HTTP) and implements the controlled fallback
// ladder.
type Resolver struct {
	cfg    ResolverConfig
	log    *zap.Logger
	ytdlp  *coredl.YTDLPDownloader
	httpDl *coredl.HTTPDownloader
	// metrics is optional — nil disables emission (nil-safe per
	// incDownloadPath guard).
	metrics *Metrics
	// limitMu serializes the daily-limit check + audit insert so that
	// concurrent downloads in the same process cannot overshoot the
	// configured limit. Cross-process races are still possible; the
	// limit is best-effort across multiple replicas.
	limitMu sync.Mutex
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
		ytdlp:   coredl.NewYTDLP(cfg),
		httpDl:  coredl.NewHTTPDownloader(resolverCfg.HTTPDownloadTimeout),
		metrics: metrics,
	}
}

// Download is the artapp.Downloader port entry point.
//
// Routes through the unified resolvePath ladder and delegates to the
// selected transport. Retries non-4xx transport failures with the
// configured backoff policy.
//
// Acquisition-mode enforcement (P0 legal/compliance, July 2026):
//   - manual_import: automatic downloads are rejected immediately.
//   - authorized_api: the daily per-account limit is checked before
//     every download; successful fetches are audited.
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

	// Acquisition-mode gate. This is the single canonical place where
	// automatic Artlist downloads are allowed or rejected.
	//
	// Fase 6 / Commit 1 (July 2026): the sentinel is now named
	// ErrAcquisitionModeBlocked (was ErrManualImportActive). The
	// rename matches the user-spec literal — the behaviour is
	// identical: manual_import block IS a fail-closed typed error,
	// surfaced via errors.Is so the orchestrator (stageProcessBatch)
	// stamps RunTagItem.Status = "blocked_mode" + item.Error verbatim,
	// and the run-state aggregate (resp.Failed++) so
	// EvaluateRunState verdicts PARTIAL_SUCCESS / FAILED on partial /
	// total block. No silent skip.
	mode := r.cfg.AcquisitionMode.Normalize()
	if !mode.AllowsAutomaticDownload() {
		return nil, artapp.ErrAcquisitionModeBlocked
	}

	// Daily limit gate. A limit of 0 means automatic downloads are
	// disabled even in authorized_api mode.
	if r.cfg.DailyDownloadLimit <= 0 {
		return nil, artapp.ErrAutomaticDownloadsDisabled
	}
	if r.cfg.AuditRepository == nil {
		return nil, fmt.Errorf("%w: audit repository required for authorized_api mode", artapp.ErrUnavailable)
	}

	// Serialize the limit check + audit insert so concurrent downloads
	// in the same process cannot overshoot the daily quota.
	r.limitMu.Lock()
	count, err := r.cfg.AuditRepository.CountDailyDownloads(ctx, "artlist", r.cfg.AccountID)
	if err != nil {
		r.limitMu.Unlock()
		return nil, fmt.Errorf("artlist resolver: daily download count failed: %w", err)
	}
	if count >= r.cfg.DailyDownloadLimit {
		r.limitMu.Unlock()
		return nil, artapp.ErrDailyDownloadLimitExceeded
	}
	// Record the audit row before performing the actual download so
	// that another concurrent download cannot squeeze in between the
	// count and the eventual record. The row starts as pending and is
	// updated to succeeded/failed after the transport completes.
	auditID, auditErr := r.cfg.AuditRepository.RecordDownload(ctx, artapp.DownloadAuditRecord{
		AssetID:     firstNonEmpty(req.ClipID, req.Filename, req.SourceRef),
		ExternalURL: req.SourceRef,
		AccountID:   r.cfg.AccountID,
		Provider:    "artlist",
		Status:      artapp.DownloadAuditStatusPending,
	})
	if auditErr != nil {
		r.limitMu.Unlock()
		return nil, fmt.Errorf("artlist resolver: failed to record download audit: %w", auditErr)
	}
	r.limitMu.Unlock()

	// Helper to finalize the audit row. Captures the auditID by value.
	markAudit := func(status artapp.DownloadAuditStatus) {
		if updateErr := r.cfg.AuditRepository.UpdateDownloadStatus(ctx, auditID, status); updateErr != nil && r.log != nil {
			r.log.Warn("resolver: failed to update download audit status",
				zap.String("audit_id", auditID),
				zap.String("status", string(status)),
				zap.Error(updateErr),
			)
		}
	}

	outPath := filepath.Join(req.DestinationID, req.Filename)
	if mkErr := os.MkdirAll(req.DestinationID, 0o755); mkErr != nil {
		markAudit(artapp.DownloadAuditStatusFailed)
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

	_, err = retry.DoWithValue(ctx, func() (struct{}, error) {
		switch path {
		case downloadPathScraper:
			r.metrics.incDownloadPath(PathBrowser)
			return struct{}{}, r.downloadViaScraper(ctx, req, outPath)
		case downloadPathHTTP:
			r.metrics.incDownloadPath(PathHTTP)
			httpReq := &coredl.HTTPDownloadRequest{
				URL:        req.SourceRef,
				OutputPath: outPath,
			}
			return struct{}{}, r.httpDl.Download(ctx, httpReq)
		case downloadPathYTDLP:
			r.metrics.incDownloadPath(PathYTDLP)
			dlReq := &coredl.DownloadRequest{
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
		markAudit(artapp.DownloadAuditStatusFailed)
		return nil, mapError(err, path == downloadPathYTDLP)
	}

	// PR-HLS-AES128 (P1, July 2026): os.Stat MUST happen BEFORE
	// markAudit(Succeeded). A 0-byte or missing file is not a success —
	// it is a sign of truncated transport or corrupt source. Pre-PR
	// these flipped the audit row to succeeded even when the actual
	// bytes on disk were missing or empty (godlike/07
	// no-fake-availability violation). See resolver_test.go
	// ::TestResolver_Download_AuditFailedOnZeroByteFile for the
	// regression lock.
	info, statErr := os.Stat(outPath)
	if statErr != nil {
		markAudit(artapp.DownloadAuditStatusFailed)
		return nil, fmt.Errorf("%w: stat result: %v", artapp.ErrEmptyResult, statErr)
	}
	if info.Size() == 0 {
		markAudit(artapp.DownloadAuditStatusFailed)
		return nil, fmt.Errorf("%w: file %q is 0 bytes (transport reported success but produced no bytes)", artapp.ErrEmptyResult, outPath)
	}

	// PR-HLS-AES128 (P1, July 2026): PostValidator is the Go-side gate
	// for ffprobe-based sanity checking. The composition root wires
	// this to ffmpeg.Processor.Probe in production so a corrupted-but-
	// stat-positive MP4 (e.g. AES-128 key not applied to segments by
	// the upstream Node scraper) fails the audit instead of silently
	// succeeding. Nillable so the existing test surface works without
	// production wiring.
	if r.cfg.PostValidator != nil {
		if vErr := r.cfg.PostValidator(ctx, outPath); vErr != nil {
			markAudit(artapp.DownloadAuditStatusFailed)
			return nil, fmt.Errorf("%w: post-validator (ffprobe-equivalent): %v", artapp.ErrInvalidResponse, vErr)
		}
	}

	// FINAL DEFENSIVE-STAGE marker: only reach here if EVERY post-check
	// passed (stat success + size > 0 + validator nil/error). Flipping
	// the audit row to succeeded at this point means the bytes on disk
	// are real, non-empty, AND (when wired) verified readable.
	markAudit(artapp.DownloadAuditStatusSucceeded)

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
