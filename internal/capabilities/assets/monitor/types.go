// Package monitor — shared DTOs and constructor dependencies.
package monitor

import (
	"go.uber.org/zap"

	channels "github.com/Marcuss-ops/PipelineGen/internal/capabilities/channels"
	ytdomain "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
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

// MonitorPorts bundles the narrow ports consumed by ChannelMonitor so
// CompositionDeps stays under the archcheck 8-field cap.
type MonitorPorts struct {
	Ytdlp       MonitorDownloaderPort
	Transcript  TranscriptProvider
	Analyzer    VideoAnalyzer
	Enqueuer    JobEnqueuer
	Discoveries YoutubeDiscoveriesPort
}

// CompositionDeps is the ctor payload for NewChannelMonitor.
type CompositionDeps struct {
	Cfg         *config.Config
	ChannelsSvc *channels.Service
	Log         *zap.Logger
	Policy      *MonitorRuntimePolicy
	Ports       MonitorPorts
	// MetricsRecorder (FASE 3.7 Commit 2, 2026-07-04): optional
	// Prometheus-shaped counter/histogram recorder. nil-safe —
	// the ctor installs a NoopMetricsRecorder default so tests
	// + partial-deploy paths don't need to wire it. Production
	// composition (lifecycle.go) wires the concrete
	// *observability.ObservabilityMetricsRecorder.
	MetricsRecorder MetricsRecorder
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
