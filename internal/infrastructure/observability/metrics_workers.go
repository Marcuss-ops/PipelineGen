package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// Worker Polling Backoff Metrics (PR-Polling / ADR-0002 §D6.5, June 2026)
	WorkerIdleTicksTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "worker_idle_ticks_total",
		Help: "Total number of worker poll-loop iterations that returned an empty ClaimNext (queue empty).",
	})

	WorkerBackoffEventsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "worker_backoff_events_total",
		Help: "Total number of worker backoff escalations (currentBackoff doubled, capped at MaxBackoff).",
	})

	WorkerWakeOnEnqueueTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "worker_wake_on_enqueue_total",
		Help: "Total number of worker poll iterations terminated early by the QueueNotifier wake broadcast.",
	})

	// Channel Monitor Metrics
	ChannelMonitorVideosChecked = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "channel_monitor_videos_checked_total",
		Help: "Total number of videos checked by the channel monitor, by channel",
	}, []string{"channel"})

	ChannelMonitorVideosWithSegments = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "channel_monitor_videos_with_segments_total",
		Help: "Videos where at least one segment was found, by channel",
	}, []string{"channel"})

	ChannelMonitorSegmentsFound = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "channel_monitor_segments_found_total",
		Help: "Total number of segments found by Gemma, by channel",
	}, []string{"channel"})

	ChannelMonitorSegmentsPerVideo = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "channel_monitor_segments_per_video",
		Help:    "Distribution of segments found per video by channel",
		Buckets: []float64{0, 1, 2, 3, 4, 5, 6, 8, 10},
	}, []string{"channel"})

	// Blocco 4 Step 2 (June 2026): the dual-budget pair that replaces
	// the monolithic acceptedCount atomic.Int32 inside
	// ChannelMonitor.checkChannel. AnalysisReservations = number of
	// MaxVideosPerRun slots consumed (TryReserve success path);
	// SuccessfulEnqueues = number of jobs that actually landed in
	// the broker (in Step 2 the two counters are equal because the
	// succeed path runs lockstep; Step 3 splits the lockstep so
	// rollback no-enqueue paths leave AnalysisReservations incremented
	// without a matching SuccessfulEnqueues++).
	ChannelMonitorAnalysisReservations = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "channel_monitor_analysis_reservations_total",
		Help: "MaxVideosPerRun slots consumed by the channel monitor filter chain, by channel. Replaces the legacy acceptedCount metric.",
	}, []string{"channel"})

	ChannelMonitorSuccessfulEnqueues = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "channel_monitor_successful_enqueues_total",
		Help: "youtube_clip.extract jobs that actually landed in the job broker, by channel. Step 2 lockstep with analysisReservations; Step 3 will diverge on rollback paths.",
	}, []string{"channel"})

	// Zero-Legacy §07 deprecation metrics (PR 9, June 2026)
	CurateLegacyInvocationsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "curate_legacy_invocations_total",
		Help: "Monotonic counter for the deprecated MediaCurator.Curate entry point (DL-CURATIONTYPES-001).",
	}, []string{"source"})
)
