// Package monitor — shared DTOs and constructor dependencies.
package monitor

import (
	"go.uber.org/zap"

	channels "github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	ytdomain "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// Priority levels for batch channel scheduling.
const (
	PriorityHot    = 1
	PriorityNormal = 2
	PriorityCold   = 3
)

// DefaultPlaylistEnd is the global default for how many videos to scan per channel check.
const DefaultPlaylistEnd = 50

// MonitorRuntimeDeps owns scheduler configuration and channel-state authority.
type MonitorRuntimeDeps struct {
	Cfg         *config.Config
	ChannelsSvc *channels.Service
	Log         *zap.Logger
	Policy      *MonitorRuntimePolicy
}

// MonitorProcessingDeps owns discovery, transcript analysis and job emission.
type MonitorProcessingDeps struct {
	Ytdlp      MonitorDownloaderPort
	Transcript TranscriptProvider
	Analyzer   VideoAnalyzer
	Enqueuer   JobEnqueuer
}

// MonitorPersistenceDeps owns durable discovery state and observability.
type MonitorPersistenceDeps struct {
	Discoveries YoutubeDiscoveriesPort
	// MetricsRecorder is optional. The constructor installs a
	// NoopMetricsRecorder for test and partial-deploy paths.
	MetricsRecorder MetricsRecorder
}

// CompositionDeps is the ctor payload for NewChannelMonitor. The three
// embedded capability bundles keep the construction contract explicit while
// ensuring every dependency group remains below the architecture field cap.
type CompositionDeps struct {
	MonitorRuntimeDeps
	MonitorProcessingDeps
	MonitorPersistenceDeps
}

// ChannelCheckResult is the typed payload returned by ChannelMonitor.checkChannel.
type ChannelCheckResult struct {
	VideosDiscovered       int
	VideosEnqueued         int
	VideosSkipped          int
	VideosAlreadyScheduled int
	VideosRejected         int
	InfraFailures          int
}

// EnqueueOutcome is the typed label for a single video's per-cycle disposition.
type EnqueueOutcome string

const (
	OutcomeEnqueued         EnqueueOutcome = "enqueued"
	OutcomeAlreadyScheduled EnqueueOutcome = "already_scheduled"
	OutcomeRejected         EnqueueOutcome = "rejected"
	OutcomeInfraFailure     EnqueueOutcome = "infra_failure"
)

// Analysis is the analyzer result consumed by enqueue orchestration.
type Analysis struct {
	Score          int
	MatchedKeyword string
	Category       string
	Segments       []ytdomain.Segment
}
