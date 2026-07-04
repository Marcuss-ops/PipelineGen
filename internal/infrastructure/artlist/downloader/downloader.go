// Package downloader owns the Artlist HTTP/HLS download pipeline.
// It implements artlist.Downloader (PR2.1) by detecting HLS vs
// progressive content and delegating to the canonical
// internal/infrastructure/downloader (yt-dlp + HTTPDownloader). It
// owns the user-agent contract, the retry policy, and the
// FFmpeg-via-yt-dlp merge step.
//
// PR2.3: the application layer (internal/application/assets/providers/artlist) no
// longer imports os/exec, builds yt-dlp arg lists, or chooses HTTP.
// The orchestrator wires this package via the new artlist.Downloader
// port and stays agnostic of the underlying transport.
//
// godlike/06 SSOT — dual download surface (PR-ART-002 P0.2, July 2026).
//
// PipelineGen has TWO Artlist download surfaces by design: this package
// is the **primary, canonical** surface for direct progressive MP4 /
// MOV and simple HLS (.m3u8) streams where httprange + yt-dlp cookie
// impersonation succeeds. It is wired by the composition root at
// `internal/app/build_bundles_artlist.go::WireArtlist` via
// `downloader.New(cfg, downloader.Config{})` + `NewArtlistStager` (so
// the orchestrator's `assets.SourceStager.StageSource` route calls
// this surface first). When this surface succeeds, the staged
// `*assets.StagedAsset.LocalPath` is populated, and the
// mediaProcessor short-circuits its own download path (no fallback
// fired).
//
// The **fallback** surface for lazy-loaded, JS-gated HLS streams
// (Artlist CDN URLs that REQUIRE Chromium DOM interaction + a real
// browser session state — yt-dlp fails with 403 because the HLS
// segments are ACL'd to the browser's session) lives in two places:
//
//  1. Go dispatcher: `internal/infrastructure/media/processor/processor_download.go`
//     `isArtlistURL` + `downloadViaScraper` functions. These are
//     transitively reached from `mediaProcessor.Process` via the
//     post-Process `if input.LocalPath == ""` branch that
//     conditionally calls `downloadStep` (the exact line in
//     `processor.go` was not re-verified in this PR — use function-
//     name anchors when reading this godoc; line numbers drift as
//     `Process` evolves). The fallback fires only when input is
//     detected as Artlist-shaped (isArtlistURL || isArtlistNumericID
//     per the urls/name/ID triangulation). The primary short-circuit
//     in `Process` (verified `processor.go:146` — the "Step 9/12
//     wire-up (July 2026): when input.LocalPath != "", the download
//     step is bypassed" comment) skips the entire `downloadStep`
//     call when `input.LocalPath != ""`.
//  2. Node implementation: `node-scraper/src/artlist/download.js` +
//     `node-scraper/artlist_server.js` `/download` HTTP endpoint.
//     Called by (1) via POST to `<scraperURL>/download`.
//
// **Divider (the rule that picks between them)**: the orchestrator at
// `internal/application/assets/providers/artlist/run_orchestrator_stages.go`
// ::stageProcessBatch ALWAYS tries the Go-primary path first via
// `o.svc.stager.StageSource(ctx, assets.SourceRef{URL: …})`. If it
// succeeds, the staged `*assets.StagedAsset{LocalPath: ...}` is
// threaded into the `mediaProcessor.Process` call as
// `*asset.ProcessInput{LocalPath: ...}`; `Process` then short-circuits
// at `internal/infrastructure/media/processor/processor.go:146` and
// skips the `downloadStep` invocation entirely (so `downloadViaScraper`
// — the Node fallback — is also never called). If StageSource fails
// (graceful degrade; the orchestrator logs the failure with its
// standard `Warn(stager.StageSource failure, ...)` shape), the
// mediaProcessor takes over; for Artlist-shaped URLs it dispatches
// to `downloadViaScraper` (Node Puppeteer). The two surfaces compose
// a defense-in-depth ladder — they NEVER race on the same
// invocation: the no-race guarantee is a per-invocation sequential
// gate via the `Process` short-circuit at `processor.go:146` (no
// `sync.Mutex` involved; do not grep for one).
//
// **godlike/07 no-fake-availability audit**: when this surface fails
// AND the fallback fires, ops see the warning log
// `"Artlist scraper download failed, falling back to yt-dlp"` in
// `processor_download.go::downloadStep` — every fallback invocation
// is observable in the log stream, never silent.
//
// **godlike/06 forward-pointer (PR-ARTLIST-DOWNLOAD-SURFACE-UNIFY,
// deadline 2026-08-15)**: the duplicated HLS detection (`isHLS` here
// vs `isHLSURL` in `processor_download.go`) + dual-auth strategy
// (hardcoded `/tmp/artlist_cookies.txt` in Go vs real browser session
// in Node) is a recognized SSOT smell (one fact, two owners). PR-
// ARTLIST-DOWNLOAD-SURFACE-UNIFY will collapse to ONE canonical
// surface (likely: Node scraper becomes SSOT for browser-auth-only
// HLS, this Go surface stays SSOT for progressive direct + simple
// m3u8). Until that PR lands, operators reading this package should
// understand that "`artlist.Downloader`" is the canonical entrypoint
// for the bulk of `/run` invocations; the Node `/download` endpoint
// exists ONLY as a defense-in-depth fallback for browser-auth-only
// assets.
//
// **See also**: `architecture/current.yaml#ART-002` (TODO: file
// wave-tracker entry in lockstep with PR-ARTLIST-DOWNLOAD-SURFACE-
// UNIFY landing — the forward-pointer anchor does not exist yet on
// `architecture/current.yaml`); the godlike/06 SSOT discipline
// (CANONICAL.md §1 + CHANGELOG audit-pins); CHANGELOG.md
// `## Unreleased → ART-002 P0.2 closure meta-entry`.
//
// **Honest scope-lock (godlike/07)**: this is a documentation-only
// commit; no code path, retry policy, or fallback wiring was
// modified. The dual-surface architecture is the SSOT audit-pin that
// the follow-up unification PR will dissolve.

// Out of scope here (post-PR2):
//   - moving the literal "artlist" impersonation block out of
//     infrastructure/downloader.Download into this package (the
//     block currently fires transparently when the URL contains
//     "artlist"; a follow-up PR should move it for full isolation).
//   - rotating the Artlist cookies file — currently a hardcoded
//     "/tmp/artlist_cookies.txt"; pending a tokens module.
package downloader

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	artapp "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	core_dl "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// Config carries the wiring the application passes to New. All fields
// are optional; zero values fall back to the defaults below.
type Config struct {
	// MaxAttempts is the retry budget per Download call. Default: 3.
	MaxAttempts int
	// InitialBackoff is the delay before the first retry. Default: 1s.
	InitialBackoff time.Duration
	// MaxBackoff caps the exponential backoff. Default: 30s.
	MaxBackoff time.Duration
	// JitterFraction adds randomness in [0, 1) to each retry's sleep.
	// 0 disables jitter. Default: 0.
	JitterFraction float64
	// HTTPDownloadTimeout is the per-byte timeout for the progressive
	// HTTP fallback. Default: 5m.
	HTTPDownloadTimeout time.Duration
}

// Provider implements artlist.Downloader for Artlist assets.
// It composes the canonical yt-dlp + HTTPDownloader and applies the
// retry policy + sentinel mapping on top.
type Provider struct {
	cfg    Config
	ytdlp  *core_dl.YTDLPDownloader
	httpDl *core_dl.HTTPDownloader
}

// New constructs a Provider by composing the canonical yt-dlp +
// HTTPDownloader. cfg is mandatory (drives yt-dlp path, cookies, JS
// runtime). downloadCfg is optional; zero values fall back to defaults.
func New(cfg *config.Config, downloadCfg Config) *Provider {
	if downloadCfg.MaxAttempts <= 0 {
		downloadCfg.MaxAttempts = 3
	}
	// Default jitter avoids thundering-herd retry storms when multiple
	// workers hit the same upstream at once (Artlist 5xx bursts).
	if downloadCfg.JitterFraction <= 0 {
		downloadCfg.JitterFraction = 0.3
	}
	if downloadCfg.InitialBackoff <= 0 {
		downloadCfg.InitialBackoff = time.Second
	}
	if downloadCfg.MaxBackoff <= 0 {
		downloadCfg.MaxBackoff = 30 * time.Second
	}
	if downloadCfg.HTTPDownloadTimeout <= 0 {
		downloadCfg.HTTPDownloadTimeout = 5 * time.Minute
	}
	return &Provider{
		cfg:    downloadCfg,
		ytdlp:  core_dl.NewYTDLP(cfg),
		httpDl: core_dl.NewHTTPDownloader(downloadCfg.HTTPDownloadTimeout),
	}
}

// Compile-time interface assertion: this package is forced to satisfy
// the application port.
var _ artapp.Downloader = (*Provider)(nil)

// Download is the artlist.Downloader port entry point.
//
// Picks HLS or progressive HTTP based on the SourceRef. Retries
// non-4xx transport failures. Maps the underlying error to one of
// the centralized sentinels.
//
// DestinationID is treated as the directory under which Filename is
// staged (filepath.Join). MkdirAll is called so the caller can pass
// a path it has not pre-created.
func (p *Provider) Download(ctx context.Context, req artapp.DownloadRequest) (*artapp.DownloadResult, error) {
	if strings.TrimSpace(req.SourceRef) == "" {
		return nil, fmt.Errorf("%w: source ref required", artapp.ErrEmpty)
	}
	if strings.TrimSpace(req.Filename) == "" {
		return nil, fmt.Errorf("%w: filename required", artapp.ErrEmpty)
	}
	// Filename must be a plain relative path under DestinationID.
	// Reject absolute paths and any input that would clean to a
	// different string (catches "../x", "a/../b" without rejecting
	// legitimate dot-containing names like "foo..bar").
	if filepath.IsAbs(req.Filename) {
		return nil, fmt.Errorf("%w: filename must not be absolute", artapp.ErrInvalidResponse)
	}
	// Reject "." too: filepath.Clean(".") == "." and HasPrefix(".", "../") is
	// false, so it would otherwise slip through and collide with DestinationID.
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

	isHLS := strings.Contains(req.SourceRef, ".m3u8")

	opts := retry.Options{
		MaxAttempts:    p.cfg.MaxAttempts,
		InitialBackoff: p.cfg.InitialBackoff,
		MaxBackoff:     p.cfg.MaxBackoff,
		JitterFraction: p.cfg.JitterFraction,
		IsRetryable:    retry.IsTransient,
	}

	_, err := retry.DoWithValue(ctx, func() (struct{}, error) {
		if isHLS {
			dlReq := &core_dl.DownloadRequest{
				URL:        req.SourceRef,
				OutputPath: outPath,
				// Format, MergeFormat, UseCookies inherit yt-dlp defaults.
				// The artlist impersonation block in core_dl.Download fires
				// when the URL contains "artlist"; exactly what we want
				// here (see OutOfScope note in the package doc).
			}
			return struct{}{}, p.ytdlp.Download(ctx, dlReq)
		}
		httpReq := &core_dl.HTTPDownloadRequest{
			URL:        req.SourceRef,
			OutputPath: outPath,
		}
		return struct{}{}, p.httpDl.Download(ctx, httpReq)
	}, opts)

	if err != nil {
		return nil, mapError(err, isHLS)
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

// mapError classifies an underlying transport error into one of the
// centralized artapp sentinels so callers branch on intent, not on
// transport jargon (yt-dlp stderr, http.Client codes, etc.).
func mapError(err error, isHLS bool) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case errors.Is(err, context.DeadlineExceeded), strings.Contains(msg, "timeout"):
		return fmt.Errorf("%w: %v", artapp.ErrTimeout, err)
	case strings.Contains(msg, "bad status: 429"):
		// Rate limit: back off + retry, or surface to orchestrator so
		// it can try the next source.
		return fmt.Errorf("%w: %v", artapp.ErrTransportFallback, err)
	case strings.Contains(msg, "bad status: 404"), strings.Contains(msg, "no playable video"):
		return fmt.Errorf("%w: %v", artapp.ErrNotFound, err)
	case strings.Contains(msg, "bad status: 4"):
		return fmt.Errorf("%w: %v", artapp.ErrInvalidResponse, err)
	case strings.Contains(msg, "bad status: 5"):
		// 5xx usually signals the source is broken. Surface as transport
		// fallback so the orchestrator above this port can try the next
		// Downloader in the chain.
		return fmt.Errorf("%w: %v", artapp.ErrTransportFallback, err)
	}
	// Network errors, yt-dlp crashes, unknown → unavailable / transport
	// fallback. For HLS hangs we lean ErrTransportFallback because
	// yt-dlp's exit code is the more useful "try another downloader"
	// signal than "the source is malformed".
	if isHLS {
		return fmt.Errorf("%w: %v", artapp.ErrTransportFallback, err)
	}
	return fmt.Errorf("%w: %v", artapp.ErrUnavailable, err)
}
