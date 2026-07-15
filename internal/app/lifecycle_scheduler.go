// Package app owns scheduler-mode lifecycle composition.
package app

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/monitor"
	"github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	semantic "github.com/Marcuss-ops/PipelineGen/internal/application/semantic"
	transcripts "github.com/Marcuss-ops/PipelineGen/internal/application/transcripts"
	monitoradapter "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/adapters/monitoradapter"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/artlist/health"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ytdlp"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"

	"go.uber.org/zap"
)

type schedulerDeps struct {
	cfg  *config.Config
	root *ComposeRoot
	log  *zap.Logger
}

// buildSchedulerSteps creates scheduler-owned startup steps without launching
// goroutines during composition.
func buildSchedulerSteps(deps schedulerDeps) (*monitor.ChannelMonitor, []StartupStep) {
	var steps []StartupStep
	var channelMon *monitor.ChannelMonitor

	if deps.cfg.Jobs.EnableChannelMonitor {
		channelsSvc := channels.NewService(
			channels.NewRepositoryAdapter(assets.NewChannelsRepository(deps.root.DB.DB)),
			deps.log,
		)
		ytdlpForSubtitles := downloader.NewYTDLP(deps.cfg)
		ytdlpSubtitleAdapter := transcripts.NewYTDLPSubtitleAdapter(transcripts.Deps{
			Ytdlp:      ytdlpForSubtitles,
			CmdBuilder: ytdlp.NewCommandBuilder(deps.cfg),
			UseCookies: true,
			Log:        deps.log,
		})
		ollamaAnalyzer := semantic.NewOllamaAnalyzer(semantic.Deps{
			OllamaClient:    deps.root.AI.OllamaClient,
			Subtitles:       ytdlpSubtitleAdapter,
			Log:             deps.log,
			Model:           deps.cfg.External.OllamaModel,
			DataDir:         deps.cfg.Storage.DataDir,
			DefaultCategory: "general",
		})

		channelMon = monitor.NewChannelMonitor(monitor.CompositionDeps{
			MonitorRuntimeDeps: monitor.MonitorRuntimeDeps{
				Cfg:         deps.cfg,
				ChannelsSvc: channelsSvc,
				Log:         deps.log,
			},
			MonitorProcessingDeps: monitor.MonitorProcessingDeps{
				Ytdlp:      newMonitorYtdlpAdapter(ytdlpForSubtitles),
				Transcript: ytdlpSubtitleAdapter,
				Analyzer:   ollamaAnalyzer,
				Enqueuer:   monitoradapter.NewExtractionIntentAdapter(deps.root.Jobs.Service, channelsSvc, deps.log),
			},
			MonitorPersistenceDeps: monitor.MonitorPersistenceDeps{
				Discoveries: newMonitorDiscoveriesAdapter(assets.NewYoutubeDiscoveriesRepository(deps.root.DB.DB)),
				MetricsRecorder: observability.NewObservabilityMetricsRecorder(
					observability.ChannelMonitorVideosChecked,
					observability.ChannelMonitorVideosWithSegments,
					observability.ChannelMonitorSegmentsFound,
					observability.ChannelMonitorSegmentsPerVideo,
				),
			},
		})

		cm := channelMon
		steps = append(steps, StartupStep{
			Name:     "channel-monitor",
			Required: false,
			Start: func(startCtx context.Context) error {
				concurrent.SafeGo("channel-monitor", func() { cm.Start(startCtx) })
				deps.log.Info("Channel monitor started")
				return nil
			},
			Stop: func(_ context.Context) error { return nil },
		})
	}

	if deps.cfg.Features.ArtlistEnabled && deps.cfg.External.ArtlistScraperServerURL != "" {
		artlistProbe := health.New(
			deps.cfg.External.ArtlistScraperServerURL,
			deps.log,
			&health.Options{
				Interval:         health.DefaultProbeInterval,
				FailureThreshold: health.DefaultFailureThreshold,
				Metrics:          health.NewMetrics(),
			},
		)
		ap := artlistProbe
		steps = append(steps, StartupStep{
			Name:     "artlist-scraper-health-probe",
			Required: false,
			Start: func(startCtx context.Context) error {
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
			Stop: func(stopCtx context.Context) error { return ap.Stop(stopCtx) },
		})
	}

	if deps.root.Domains.YoutubeClipService != nil {
		steps = append(steps,
			StartupStep{
				Name:     "yt-cache-prewarm",
				Required: false,
				Start: func(context.Context) error {
					return fmt.Errorf("yt-cache-prewarm: %w", ErrCapabilityDisabled)
				},
				Stop: func(context.Context) error { return nil },
			},
			StartupStep{
				Name:     "yt-nightly-prewarm",
				Required: false,
				Start: func(context.Context) error {
					return fmt.Errorf("yt-nightly-prewarm: %w", ErrCapabilityDisabled)
				},
				Stop: func(context.Context) error { return nil },
			},
		)
	}

	return channelMon, steps
}
