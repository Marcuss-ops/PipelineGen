// Package app — lifecycle scheduler capability (PR-LIFECYCLE-SPLIT-BY-CAPABILITY, July 2026).
//
// Extracted from internal/app/lifecycle.go per AGENTS.md Pattern 5.
// Owns the scheduler-mode startup steps:
//
//   - channel-monitor      (monitor.NewChannelMonitor with FASE 3.7
//     composition-root adapter wiring)
//   - yt-cache-prewarm     (ErrCapabilityDisabled sentinel, Phase 2+ follow-up)
//   - yt-nightly-prewarm   (ErrCapabilityDisabled sentinel, Phase 2+ follow-up)
//
// Sister file to lifecycle_worker.go + lifecycle_maintenance.go (the 3
// capability files) + lifecycle_adapters.go (composition-root adapters).
//
// buildSchedulerSteps returns the channel-monitor pointer alongside the
// steps so the orchestrator (lifecycle.go) can surface it to
// backgroundJobs.channelMonitor for graceful teardown in shutdown.go
// (channelMonitor.Stop is the only explicit shutdown call in the
// lifecycle-runtime-ownership wave, June 2026).
package app

import (
	"context"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/monitor"
	"github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	semantic "github.com/Marcuss-ops/PipelineGen/internal/application/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/transcripts"
	monitoradapter "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/adapters/monitoradapter"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/artlist/health"
	sqlchannels "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/youtubediscoveries"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
	platformyoutube "github.com/Marcuss-ops/PipelineGen/internal/platform/youtube"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ytdlp"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// schedulerDeps holds the composition-root dependencies required to
// build the scheduler-mode startup steps. Typed, not any:
// mirrors the jobRunnerDeps + workerDeps pattern.
type schedulerDeps struct {
	cfg  *config.Config
	root *wiring.ComposeRoot
	log  *zap.Logger
}

// buildSchedulerSteps returns the scheduler-mode StartupStep list
// (channel-monitor + the 2 ErrCapabilityDisabled yt prewarms) plus the
// *monitor.ChannelMonitor pointer for graceful teardown in
// shutdown.go. Returns (nil, nil) when cfg.Jobs.EnableChannelMonitor
// is false AND no YoutubeClipService is wired (caller must handle the
// nil case — graceful no-op).
//
// godlike/06 SSOT: the channel-monitor construction is the canonical
// place to wire the FASE 3.7 monitor/ ports (Ytdlp + Discoveries +
// MetricsRecorder) — these are the only infra-leak surfaces that
// lifecycle.go was previously the sole owner of. The composition-root
// adapter pattern (per AGENTS.md Pattern 0) keeps the layering
// (application → infra) intact: monitor owns canonical sentinels +
// DTOs; infra owns its own; the composition root translates.
func buildSchedulerSteps(deps schedulerDeps) (*monitor.ChannelMonitor, []StartupStep) {
	var steps []StartupStep
	var channelMon *monitor.ChannelMonitor

	if deps.cfg.Jobs.EnableChannelMonitor {
		// PR 2 (June 2026): channels are loaded exclusively from
		// category_channels via channels.Service. The raw *sql.DB is
		// replaced by the canonical channels service which is the
		// single source of truth for channel configuration.
		channelsSvc := channels.NewService(
			channels.NewRepositoryAdapter(sqlchannels.NewChannelsRepository(deps.root.DB.DB)),
			deps.log,
		)
		// Step 9 commit 2 (June 2026): wire the concrete
		// YTDLPSubtitleAdapter (os/exec + VTT regex) and
		// OllamaAnalyzer (Score + Classify + FindSegments) as the
		// monitor's Transcript + Analyzer ports.
		//
		// PR-WIRE-SUBTITLE-FETCHER-ADAPTER (2026-07-06): inject
		// ytdlp.NewCommandBuilder(deps.cfg) + UseCookies: true so
		// the monitor's subtitle fetch delegates the canonical
		// yt-dlp argv prefix to ytdlp.BaseArgs (same Pattern 0
		// port the infrastructure-layer SubtitleFetcherAdapter
		// uses post PR-SUBTITLES-BASEARGS-MIGRATION). Pre-PR the
		// adapter manually appended --write-auto-subs / --write-subs
		// / --skip-download and bypassed the helper, dropping
		// --cookies (required for n-challenge + age-restricted
		// videos), --js-runtime + --remote-components ejs:github,
		// --no-warnings, and --extractor-args
		// youtube:player_client=web,android. UseCookies=true is
		// the right default for the monitor (it processes the full
		// channel feed including age-restricted videos).
		ytdlpForSubtitles := downloader.NewYTDLP(deps.cfg)
		ytdlpSubtitleAdapter := platformyoutube.NewYTDLPSubtitleAdapter(platformyoutube.Deps{
			Ytdlp:      ytdlpForSubtitles,
			CmdBuilder: ytdlp.NewCommandBuilder(deps.cfg),
			UseCookies: true,
			Log:        deps.log,
		})
		transcriptSource := transcripts.NewCachingTranscriptProvider(ytdlpSubtitleAdapter)
		ollamaAnalyzer := semantic.NewOllamaAnalyzer(semantic.Deps{
			OllamaClient:    deps.root.AI.OllamaClient,
			Subtitles:       transcriptSource,
			Log:             deps.log,
			Model:           deps.cfg.External.OllamaModel,
			DataDir:         deps.cfg.Storage.DataDir,
			DefaultCategory: "general",
		})

		channelMon = monitor.NewChannelMonitor(monitor.CompositionDeps{
			Cfg:         deps.cfg,
			ChannelsSvc: channelsSvc,
			Log:         deps.log,
			Ports: monitor.MonitorPorts{
				// Ytdlp wires the concrete *downloader.YTDLPDownloader so
				// monitor/discovery.go::discoverChannelVideos can call
				// ListChannel per scheduler tick. Same instance is re-used in
				// transcripts/YTDLPSubtitleAdapter for the subtitle
				// subprocess, keeping a single downloader binary+cookies
				// config across the two adapters.
				Ytdlp:      newMonitorYtdlpAdapter(ytdlpForSubtitles),
				Transcript: transcriptSource,
				Analyzer:   ollamaAnalyzer,
				Enqueuer:   monitoradapter.NewExtractionIntentAdapter(deps.root.Jobs.Service, channelsSvc, deps.log),
				// Commit 1/6 (PR-C-YouTube-Cutover, June 2026) — wiring CLOSED
				// in Commit 2 (2026-07-04). The per-video discovery ledger
				// (TryReserve + MarkEnqueued + MarkRejected + MaxDiscoveredAt)
				// is wrapped in monitorDiscoveriesAdapter (struct-embeds
				// *youtubediscoveries.YoutubeDiscoveriesRepository + overrides the
				// translation methods). See lifecycle_adapters.go for the
				// canonical adapter surface.
				Discoveries: newMonitorDiscoveriesAdapter(youtubediscoveries.NewYoutubeDiscoveriesRepository(deps.root.DB.DB)),
			},
			// FASE 3.7 Commit 2 (2026-07-04): wire the canonical
			// *observability.ObservabilityMetricsRecorder so analyzer +
			// discovery call sites invoke the package-level Prometheus
			// counters WITHOUT a direct observability import in the
			// monitor package — the adapter is the composition-root
			// bridge that keeps the layering (application → infra) intact.
			MetricsRecorder: observability.NewObservabilityMetricsRecorder(
				observability.ChannelMonitorVideosChecked,
				observability.ChannelMonitorVideosWithSegments,
				observability.ChannelMonitorSegmentsFound,
				observability.ChannelMonitorSegmentsPerVideo,
			),
		})

		// Channel monitor: optional background service.
		cm := channelMon
		steps = append(steps, StartupStep{
			Name: "channel-monitor", Required: false,
			Start: func(startCtx context.Context) error {
				concurrent.SafeGo("channel-monitor", func() { cm.Start(startCtx) })
				deps.log.Info("Channel monitor started")
				return nil
			},
			Stop: func(_ context.Context) error { return nil },
		})
	}

	// Artlist Node scraper health probe (ART-002 P1.3, July 2026):
	// 60s-tick HTTP liveness probe on the persistent Node scraper
	// server. Fires an alert (log.Warn + Prometheus counter
	// artlist_scraper_health_alerts_total) after 3 consecutive
	// transport errors (the user spec "alert 3 fallimenti
	// consecutivi"). Composition-time fail-closed (P0.1 gate
	// mirrors): the step is only registered when BOTH the Artlist
	// feature is enabled AND the scraper URL is configured
	// (mirrors validateArtlistScraperURL in build_bundles_artlist.go).
	// Otherwise the step is omitted entirely (not gated on
	// ErrCapabilityDisabled — the capability is intentionally
	// absent when Artlist is off, not "disabled at startup").
	if deps.cfg.Features.ArtlistEnabled && deps.cfg.External.ArtlistScraperServerURL != "" {
		artlistProbe := health.New(
			deps.cfg.External.ArtlistScraperServerURL,
			deps.log,
			&health.Options{
				Interval:         health.DefaultProbeInterval,    // 60s
				FailureThreshold: health.DefaultFailureThreshold, // 3
				Metrics:          health.NewMetrics(),            // promauto globals
			},
		)
		ap := artlistProbe
		steps = append(steps, StartupStep{
			Name: "artlist-scraper-health-probe", Required: false,
			Start: func(startCtx context.Context) error {
				// health.Probe.Start launches its own ticker
				// goroutine internally (no SafeGo wrap needed —
				// unlike channel-monitor whose Start blocks until
				// ctx cancellation, the probe's Start returns
				// immediately after launching the goroutine).
				if err := ap.Start(startCtx); err != nil {
					return fmt.Errorf("artlist-scraper-health-probe start: %w", err)
				}
				deps.log.Info("Artlist scraper health probe started",
					zap.String("server_url", deps.cfg.External.ArtlistScraperServerURL),
					zap.Duration("interval", health.DefaultProbeInterval),
					zap.Int("failure_threshold", health.DefaultFailureThreshold),
				)
				return nil
			},
			Stop: func(stopCtx context.Context) error {
				// health.Probe.Stop blocks until the ticker
				// goroutine confirms shutdown OR stopCtx times
				// out (the per-call budget caps the wait at
				// 5s; the goroutine exits via ctx cancellation
				// on the parent lifecycle in practice).
				return ap.Stop(stopCtx)
			},
		})
	}

	// yt-cache-prewarm + yt-nightly-prewarm (gated on YoutubeClipService).
	// These return the typed ErrCapabilityDisabled sentinel (not nil) per
	// godlike/07 no-fake-availability: a step returning nil while
	// loading NOTHING is a fake success — the operator's view of the
	// system would otherwise omit the suppressed capability. The
	// server_lifecycle Start Warn log surfaces the typed error.
	if deps.root.Domains.YoutubeClipService != nil {
		_ = deps.root.Domains.YoutubeClipService // late-bound: future Phase 2+ wiring will consume this
		steps = append(steps, StartupStep{
			Name: "yt-cache-prewarm", Required: false,
			Start: func(startCtx context.Context) error {
				return fmt.Errorf("yt-cache-prewarm: %w", ErrCapabilityDisabled)
			},
			Stop: func(_ context.Context) error { return nil },
		})
		steps = append(steps, StartupStep{
			Name: "yt-nightly-prewarm", Required: false,
			Start: func(startCtx context.Context) error {
				return fmt.Errorf("yt-nightly-prewarm: %w", ErrCapabilityDisabled)
			},
			Stop: func(_ context.Context) error { return nil },
		})
	}

	return channelMon, steps
}
