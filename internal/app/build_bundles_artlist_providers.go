// Package app — build_bundles_artlist_providers.go
//
// Artlist provider-side wiring. Lives next to, but separate from,
// the WireArtlist orchestrator (build_bundles_artlist_artlist.go)
// so that the ~6 inline construction blocks (ffmpeg processor,
// AdminSystemProber, HTTPSelfLoopProbe, downloader.Resolver + its
// PostValidator closure, ArtlistStager, media-processor adapter
// injection, Pexels/Pixabay fallback searchers) do not bloat the
// orchestrator beyond readability.
//
// godlike/06 SSOT: each provider is the SOLE canonical concrete for
// its port — no shim layer between the composition root and the
// upstream packages. The helpers below write the bundle verbatim and
// return it to `WireArtlist` which forwards into artlist.NewService.
package app

import (
	"context"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"time"

	artlist "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/artlist/diagnostics"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/artlist/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/artlist/fallback"
	ffmpeg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	mediaproc "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/processor"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

// constructArtlistProviders returns the provider-side bundle built from
// cfg + the wiring.ArtlistBundle + the auditAdapter parameter (constructor-
// order: caller must run constructArtlistRepositories first so this
// audit adapter is available before the Resolver is constructed).
//
// godlike/06 SSOT: ffmpegProc / isLiveProbe / systemProber /
// artlistDownloader / artlistStager / pexelsSearcher / pixabaySearcher
// are the SOLE canonical concretes for the artlist-provider ports.
// Duplicate construction sites elsewhere in the composition root
// would be a godlike/06 SSOT violation.
//
// godlike/07 fail-closed: the audit adapter is mandated as a parameter
// (not a once-constructed local) so the composition-order invariant
// — repos first, then providers — is enforced by the caller; an
// auditAdapter=nil parameter compiles, but at runtime the resolver's
// markAudit path will skip audit rows. This matches the original
// behavior (audit adapter was injected inline into the ResolverConfig
// in the original WireArtlist body).
//
// godlike/07 fail-closed: an empty scraper URL when artlist_enabled=true
// is REJECTED here only via the higher-level validateArtlistScraperURL
// gate (called from WireArtlist before this helper runs) — the
// underlying scraper.New() / NewResolver() still accept URL="" for
// backward compat with non-Artlist providers.
func constructArtlistProviders(
	cfg *config.Config,
	log *zap.Logger,
	bundle *wiring.ArtlistBundle,
	auditAdapter artlist.DownloadAuditRepository,
) artlistProviders {
	// PR-HLS-AES128 followup-2 (July 2026): construct the canonical ffprobe
	// Processor ONCE at composition scope (lifted out of the closure to
	// avoid per-call allocation, per code-reviewer nit-1). The Processor
	// holds only the ffmpeg-derived path; Probe runs the binary as a
	// subprocess. Fail-closed: missing ffprobe binary yields a typed
	// exec error from process.Run that the closure below forwards to
	// markAudit(Failed) + ErrInvalidResponse to the caller.
	ffmpegProc := ffmpeg.NewProcessor("")

	// godlike/06 SSOT: HTTPSelfLoopProbe is the canonical app-layer wrapper
	// for *Probe; its Probe(ctx) (bool, error) signature matches
	// artlist.IsLiveProbe exactly (http_live_probe.go). Composition root
	// owns the resolution of baseURL (cfg.External.VeloxBaseURL > localhost
	// fallback) + timeout (5s default per DefaultProbeTimeout in artlist pkg).
	probeBaseURL := cfg.External.VeloxBaseURL
	if probeBaseURL == "" {
		probeBaseURL = fmt.Sprintf("http://localhost:%d", cfg.Server.Port)
	}
	isLiveProbe := artlist.NewHTTPSelfLoopProbe(
		probeBaseURL,
		"/api/artlist/stats",
		artlist.DefaultProbeTimeout,
		log,
	)

	// godlike/06 SSOT: AdminSystemProber is the canonical concrete
	// for artlist.SystemProber (Phase 2 / Fase 2, July 2026). The
	// composition root owns the wiring of probe inputs (URLs,
	// sqlite DB, dispatcher health fn, drive folder probe fn,
	// renderer). Commit 1 (wire-shape) wires ONLY the 4 upstream
	// probe URLs that cfg currently exposes; the remaining 6
	// probes (ffmpeg_binary, drive_folder, sqlite_writable,
	// outbox_dispatcher, qdrant_reachable, embedding_provider)
	// are stubbed in the AdminSystemProber and will be replaced
	// one-by-one in subsequent commits (Commit 2/3/4). ScraperURL
	// is the only URL cfg exposes today; the other 3 upstream
	// probes fail honestly with `_url_not_configured` until a
	// Phase 2 follow-up surfaces BrowserURL/SessionURL/DownloaderURL
	// in cfg.
	systemProber := &diagnostics.AdminSystemProber{
		Log: log,
		// Commit 1: 4 upstream probes wired.
		ScraperURL:    cfg.External.ArtlistScraperServerURL,
		BrowserURL:    "", // Commit 2 follow-up: cfg.External.ArtlistBrowserURL
		SessionURL:    "", // Commit 2 follow-up: cfg.External.ArtlistSessionURL
		DownloaderURL: "", // Commit 2 follow-up: cfg.External.ArtlistDownloaderURL
		// Commit 2: 2 capability probes wired.
		//
		// godlike/06 SSOT: ffmpeg path comes from cfg.External.FfmpegPath
		// with an empty-string fallback. The prober interprets an empty
		// FFmpegBinaryPath as "use exec.LookPath("ffmpeg") to honour $PATH"
		// (matches the precedent in
		// internal/application/clips/upload/usecase.go line 361 +
		// cutter_test.go line 60 which use exec.LookPath directly on the
		// bare "ffmpeg" / "ffprobe" names).
		FFmpegBinaryPath: cfg.External.FfmpegPath,
		FFmpegRunner:     diagnostics.DefaultRunner{},
		// Drive folder probe: ProbeFolderAccess remains nil in Commit 2
		// because the canonical delivery.Publisher interface does not
		// expose ProbeFolderAccess today. A follow-up commit lifts the
		// method onto the canonical publisher port; until then the probe
		// honestly fails with Error="drive_folder_probe_unwired".
		//
		// ProbeFolderRootID IS populated (ResolveRootFolderID reads
		// cfg.Drive.ArtlistFolder()) so once ProbeFolderAccess is wired
		// the probe can run immediately with no composition-root code
		// change beyond replacing nil with a real closure.
		ProbeFolderAccess: nil,
		ProbeFolderRootID: artlist.ResolveRootFolderID(cfg),
		// Real Artlist queries against the Node scraper can take much
		// longer than the 5s default (initial Chromium launch, session
		// handshake, page navigation). Give the diagnostics battery
		// enough budget to observe the result without masking slow but
		// healthy scraper startup.
		ProbeTimeout: 2 * time.Minute,
	}

	// PR-ARTLIST-SEARCHERS (2026-07-04): construct the public-stock
	// searchers once and reuse them both for the legacy fallback chain
	// inside artlist.Service and for the new unified providerassets
	// registry. This avoids duplicate HTTP clients and keeps the two
	// surfaces observationally equivalent.
	pexelsSearcher := fallback.NewPexels(fallback.Config{
		APIKey:     cfg.External.PexelsAPIKey,
		BaseURL:    cfg.External.PexelsBaseURL,
		SourceName: "pexels",
	})
	pixabaySearcher := fallback.NewPixabay(fallback.Config{
		APIKey:     cfg.External.PixabayAPIKey,
		BaseURL:    cfg.External.PixabayBaseURL,
		SourceName: "pixabay",
	})

	// PR-ARTLIST-DOWNLOAD-SURFACE-UNIFY (July 2026): wire the unified
	// Resolver instead of the legacy Provider. Resolver consolidates
	// all three transport paths (Node scraper / yt-dlp / HTTP) into a
	// single resolvePath decision — the duplicated isArtlistURL /
	// isDirectURL / isHLSURL detection in processor_download.go is
	// superseded by the canonical routing in resolver.go.
	//
	// godlike/06 SSOT: internal/infrastructure/artlist/downloader.Resolver
	// is the SINGLE canonical owner of Artlist download routing.
	// ResolverConfig{ScraperURL: cfg.External.ArtlistScraperServerURL}
	// feeds the Node scraper path; Config defaults (3 retries, 1s/30s
	// backoff, 0.3 jitter, 5m HTTP timeout, 5m scraper timeout) are the
	// same as the legacy Provider.
	//
	// ART-002 P1.1 (July 2026): wire the Prometheus metrics surface via
	// the 4th Pattern-0 arg. NewMetrics() returns a struct pointing at
	// the promauto global observability.ArtlistDownloadPathTotal
	// (auto-registered with prometheus.DefaultRegisterer + surfaced via
	// /metrics). All 4 path labels (PathBrowser / PathYTDLP / PathHTTP /
	// PathHLS) are now fired by the unified resolver.
	artlistDownloader := downloader.NewResolver(cfg, downloader.ResolverConfig{
		ScraperURL:         cfg.External.ArtlistScraperServerURL,
		AcquisitionMode:    artlist.ArtlistAcquisitionMode(cfg.External.ArtlistAcquisitionMode),
		AccountID:          cfg.External.ArtlistAccountID,
		DailyDownloadLimit: cfg.External.ArtlistDailyDownloadLimit,
		AuditRepository:    auditAdapter,
		// PR-HLS-AES128 (P1, July 2026): wire the canonical ffprobe
		// post-validator. Every authorized_api download is now ffprobe-
		// sanity-checked BEFORE markAudit(Succeeded) can fire
		// (godlike/07 no-fake-availability). The wrapped Probe returns
		// (*MediaInfo, error); we coerce to (ctx, path) error so the
		// ResolverConfig.PostValidator signature stays clean. The
		// HasVideo gate is the spec-required "file is readable" check:
		// HasVideo=false means the bytes on disk don't look like a real
		// media file (corrupt transport, missing AES-128 stage in
		// upstream Node scraper, etc.) and the audit row MUST flip to
		// Failed instead of silently succeeding.
		//
		// Fail-closed: if ffprobe is missing on PATH, ffmpeg.Processor.Probe
		// returns an error (from process.Run's exec.LookPath), and the
		// Resolver correctly routes this to markAudit(Failed) + a typed
		// ErrInvalidResponse to the caller. Operationally: the operator
		// sees the audit-status flip and an actionable log line naming
		// ffprobe as the missing dependency.
		PostValidator: func(ctx context.Context, path string) error {
			mediaInfo, err := ffmpegProc.Probe(ctx, path)
			if err != nil {
				return err
			}
			// PR-HLS-AES128 followup-2 (reviewer nit-2): accept audio-only
			// downloads as well as video — the spec says "file is readable"
			// which a valid audio stream satisfies. The underlying Probe()
			// error path still catches corrupt containers / unparseable files
			// (godlike/07 fail-closed).
			if !mediaInfo.HasVideo && !mediaInfo.HasAudio {
				return fmt.Errorf("ffprobe: no media stream detected in %q (corrupt container or missing AES-128 stage upstream)", path)
			}
			return nil
		},
	}, log, downloader.NewMetrics())
	// Compile-time pin lives in the infra package.
	_ = (artlist.Downloader)(artlistDownloader)
	artlistStager := artlist.NewArtlistStager(artlistDownloader)

	return artlistProviders{
		FfmpegProc:        ffmpegProc,
		IsLiveProbe:       isLiveProbe,
		SystemProber:      systemProber,
		ArtlistDownloader: artlistDownloader,
		ArtlistStager:     artlistStager,
		PexelsSearcher:    pexelsSearcher,
		PixabaySearcher:   pixabaySearcher,
	}
}

// wireArtlistProcessorDownloader injects the canonical Resolver bridge
// into the media processor's narrow ArtlistDownloader port so downloadStep
// routes Artlist clips through the canonical Resolver instead of the
// legacy downloadViaScraper method.
//
// PR-ARTLIST-DOWNLOAD-SURFACE-UNIFY-CUTOVER (July 2026): the adapter is
// the SINGLE translation site per godlike/06 SSOT. godlike/07 fail-closed:
// nil bundle.MediaProcessor is silently allowed (the hook is optional)
// but the log.Info confirms the wiring landed.
func wireArtlistProcessorDownloader(
	log *zap.Logger,
	bundle *wiring.ArtlistBundle,
	artlistDownloader *downloader.Resolver,
) {
	if bundle == nil || bundle.MediaProcessor == nil {
		return
	}
	if mp, ok := bundle.MediaProcessor.(*mediaproc.Processor); ok {
		mp.SetArtlistDownloader(&artlistProcessorDownloadAdapter{resolver: artlistDownloader})
		log.Info("WireArtlist: ArtlistDownloader wired into media processor (Resolver bridge)")
	}
}

// artlistProcessorDownloadAdapter bridges the processor's narrow
// ArtlistDownloader interface to the canonical downloader.Resolver.
// SINGLE translation site per godlike/06 SSOT.
type artlistProcessorDownloadAdapter struct {
	resolver *downloader.Resolver
}

func (a *artlistProcessorDownloadAdapter) DownloadArtlistClip(
	ctx context.Context, sourceURL, clipPageURL, clipID, destDir, filename string,
) (string, error) {
	result, err := a.resolver.Download(ctx, artlist.DownloadRequest{
		SourceRef:     sourceURL,
		ClipPageURL:   clipPageURL,
		ClipID:        clipID,
		DestinationID: destDir,
		Filename:      filename,
	})
	if err != nil {
		return "", err
	}
	return result.LocalPath, nil
}
