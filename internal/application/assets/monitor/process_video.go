package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	yttypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	metrics "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"

	"go.uber.org/zap"
)

func effectivePlaylistEnd(channel channels.Channel, globalDefault int) int {
	if channel.PlaylistEnd > 0 {
		return channel.PlaylistEnd
	}
	if channel.PlaylistEnd == 0 {
		return 0
	}
	return globalDefault
}

// processVideo processes a single video from a channel check.
// PR 4 (June 2026): signature changed from (line string) to (info downloader.VideoInfo).
// PR 5 (June 2026): added min_views and duration canonical filters; enqueues jobs.
// PR 7 (June 2026): uses channels.Channel DTO directly; MonitorConfig removed.
// PR (June 2026, Blocco 4 Step 2): signature changed from
// (..., *atomic.Int32) to (..., *ChannelCounters); the tryReserve CAS
// race is now inside counters.TryReserve so process_video.go no longer
// imports sync/atomic. Step 3 will move the SuccessfulEnqueues++ out
// of TryReserve and pair it with ReleaseReservation() on the enqueue
// tail.
func (m *ChannelMonitor) processVideo(ctx context.Context, info downloader.VideoInfo, channel channels.Channel, counters *ChannelCounters) {
	videoID := info.ID
	title := info.Title

	m.log.Debug("Found video", zap.String("video_id", videoID), zap.String("title", title))

	// Hoisted before the reservation block so the dual Prometheus metric
	// pair (analysisReservations + successfulEnqueues) can label by
	// channel on the success path. Falls back to "unknown" if the
	// channel handle cannot be extracted from the channel URL.
	channelHandle := extractChannelHandle(channel.ChannelURL)
	if channelHandle == "" {
		channelHandle = "unknown"
	}

	// ── PR 5: canonical filter policy ──────────────────────────────────

	if channel.MinViews > 0 && info.Views < int64(channel.MinViews) {
		m.log.Debug("video below min_views, skipping",
			zap.String("video_id", videoID),
			zap.Int64("views", info.Views),
			zap.Int("min_views", channel.MinViews))
		return
	}

	if channel.MaxClipDuration > 0 && info.Duration > float64(channel.MaxClipDuration) {
		m.log.Debug("video exceeds max_clip_duration, skipping",
			zap.String("video_id", videoID),
			zap.Float64("duration_sec", info.Duration),
			zap.Int("max_duration", channel.MaxClipDuration))
		return
	}

	// ── Keyword filter ─────────────────────────────────────────────────
	keywords := decodeJSONStrings(channel.Keywords)
	if len(keywords) > 0 {
		if !containsAny(title, keywords) {
			m.log.Debug("title keyword no match, skipping",
				zap.String("video_id", videoID),
				zap.Strings("keywords", keywords))
			return
		}
		m.log.Debug("title keyword match", zap.String("video_id", videoID))
	}

	// ── Semantic filter ────────────────────────────────────────────────
	semanticKeywords := decodeJSONStrings(channel.SemanticKeywords)
	if len(semanticKeywords) > 0 {
		videoURL := fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)
		score, matchedKeyword, err := m.matchSemantically(ctx, videoURL, semanticKeywords, channel.MinSemanticScore)
		if err != nil {
			m.log.Warn("semantic matching failed, skipping video",
				zap.String("video_id", videoID),
				zap.Error(err))
			return
		}
		if score < semanticScoreThreshold(channel.MinSemanticScore) {
			m.log.Info("video does not match semantic keywords",
				zap.String("video_id", videoID),
				zap.String("title", title),
				zap.Int("score", score),
				zap.Int("threshold", semanticScoreThreshold(channel.MinSemanticScore)))
			return
		}
		m.log.Info("semantic match",
			zap.String("video_id", videoID),
			zap.String("title", title),
			zap.String("matched_keyword", matchedKeyword),
			zap.Int("score", score))
	}

	// ── MaxVideosPerRun gate ──────────────────────────────────────────
	// Step 3: the TryReserve and the rollback path moved INTO
	// enqueueClipExtract so the enqueue tail owns its own reservation
	// lifecycle. processVideo does not bump the dual Prometheus
	// metric pair any more — only enqueueClipExtract does, on the
	// success (RecordEnqueue) or on every rollback (no release
	// metric; Prometheus counters cannot decrement).
	//
	// processVideo still drives the VideosChecked counter so the
	// `did the filter chain permit this video to enter the enqueue
	// tail?` observability stays here; the budget observability
	// (grants + successes) moves to enqueueClipExtract.
	metrics.ChannelMonitorVideosChecked.WithLabelValues(channelHandle).Inc()
	_, _ = m.enqueueClipExtract(ctx, videoID, title, channel, counters)
}

// decodeJSONStrings decodes a JSON-encoded string array (as stored in
// the channels.Channel DTO's Keywords/SemanticKeywords fields) into a
// Go []string. Returns nil if the input is empty or unparseable.
func decodeJSONStrings(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" || s == "[]" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}

func semanticScoreThreshold(channelMin int) int {
	if channelMin > 0 {
		return channelMin
	}
	return 60
}

func extractChannelHandle(url string) string {
	if idx := strings.LastIndex(url, "@"); idx >= 0 {
		handle := url[idx+1:]
		handle = strings.TrimRight(handle, "/")
		return handle
	}
	return ""
}

// enqueueClipExtract finds segments for a video and enqueues a youtube_clip.extract job.
// PR 5 (June 2026): replaces the synchronous m.youtubeSvc.Extract() call.
// PR 6 (June 2026): Drive folder resolution moved inside extraction pipeline; dedup via job ActiveKey.
// PR 7 (June 2026): uses channels.Channel DTO directly; MonitorConfig removed.
// PR (June 2026, Blocco 4 Step 3): signature changed from
// (ctx, videoID, title, channel) to add `counters *ChannelCounters`
// and return `(EnqueueOutcome, error)`. The TryReserve gate moved
// here from processVideo so the enqueue tail owns its own
// reservation lifecycle. On every rollback path
// (noSegments / marshalErr / jobsSvc==nil / Enqueue err / ActiveKey
// collision) counters.ReleaseReservation() is called BEFORE the
// return so MaxVideosPerRun reflects the true current slot usage.
func (m *ChannelMonitor) enqueueClipExtract(ctx context.Context, videoID string, title string, channel channels.Channel, counters *ChannelCounters) (EnqueueOutcome, error) {
	channelHandle := extractChannelHandle(channel.ChannelURL)
	metricsLabel := channelHandle
	if metricsLabel == "" {
		metricsLabel = "unknown"
	}

	// ── MaxVideosPerRun TryReserve (Blocco 4 Step 3) ──────────────────
	// Returns WITHOUT touching counters (no slot was consumed):
	// SkipMaxVideosPerRun is a capacity decision, not a system error,
	// so err MUST be nil — otherwise the scheduler would feed this
	// into the exponential backoff in monitor_scheduler.go and drive
	// nextCheckTime to 24h on a budget-exhausted channel.
	if counters != nil && channel.MaxVideosPerRun > 0 {
		if !counters.TryReserve(channel.MaxVideosPerRun) {
			m.log.Debug("max_videos_per_run reached, skipping",
				zap.String("video_id", videoID),
				zap.Int("max", channel.MaxVideosPerRun))
			return EnqueueOutcome{Enqueued: false, Reason: SkipMaxVideosPerRun}, nil
		}
		// Slot consumed: bump the grants counter, NOT the successes.
		metrics.ChannelMonitorAnalysisReservations.WithLabelValues(metricsLabel).Inc()
	}

	// rollback centralises the ReleaseReservation + structured-log +
	// outcome-return pattern so every failure path below has one
	// consistent shape and SkipReason is always set.
	rollback := func(reason SkipReason, err error, msg string) (EnqueueOutcome, error) {
		if counters != nil {
			counters.ReleaseReservation()
		}
		if err != nil {
			m.log.Error(msg,
				zap.String("video_id", videoID),
				zap.String("title", title),
				zap.Error(err))
		} else {
			m.log.Info(msg,
				zap.String("video_id", videoID),
				zap.String("title", title))
		}
		return EnqueueOutcome{Enqueued: false, Reason: reason}, err
	}

	// ── Drive folder resolution (PR 6) ────────────────────────────────
	var category string
	if channel.DriveFolderID != "" && channel.Category != "" {
		category = channel.Category
	} else {
		category = m.classifyCategory(ctx, title, channel.Category, nil)
	}

	videoURL := fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)

	maxSegments := channel.MaxSegments
	if maxSegments <= 0 {
		maxSegments = 3
	}
	segments := m.findInterestingSegments(ctx, videoURL, maxSegments, channel.SegmentPrompt)

	metrics.ChannelMonitorSegmentsPerVideo.WithLabelValues(metricsLabel).Observe(float64(len(segments)))

	if len(segments) == 0 {
		return rollback(SkipNoSegments, nil, "no interesting segments found, skipping video")
	}

	metrics.ChannelMonitorVideosWithSegments.WithLabelValues(metricsLabel).Inc()
	metrics.ChannelMonitorSegmentsFound.WithLabelValues(metricsLabel).Add(float64(len(segments)))

	for idx := range segments {
		segments[idx].Name = category + " " + segments[idx].Name
	}

	driveFolderID := channel.DriveFolderID
	if driveFolderID == "" && m.cfg != nil {
		driveFolderID = m.cfg.Drive.ClipsFolder()
	}

	if m.jobsSvc == nil {
		return rollback(SkipEnqueueFailed,
			fmt.Errorf("jobsSvc not wired"),
			"jobsSvc not wired, cannot enqueue extract job")
	}

	extractReq := yttypes.ExtractRequest{
		URL:      videoURL,
		Segments: segments,
		Destination: &yttypes.DestinationRequest{
			Group:    category,
			FolderID: driveFolderID,
		},
	}
	normalize := true
	extractReq.Normalize = &normalize

	payload, err := json.Marshal(extractReq)
	if err != nil {
		return rollback(SkipEnqueueFailed, err, "failed to marshal extract request")
	}

	jobResp, err := m.jobsSvc.Enqueue(ctx, &job.EnqueueRequest{
		Type:       job.TypeYouTubeClipExtract,
		Priority:   2,
		Payload:    json.RawMessage(payload),
		MaxRetries: 3,
		ActiveKey:  "channel_sync_" + videoID,
		VideoName:  title,
	})
	if err != nil {
		return rollback(SkipEnqueueFailed, err, "failed to enqueue youtube_clip.extract job")
	}

	// ── Success path: RecordEnqueue + SuccessfulEnqueues metric ───────
	if counters != nil {
		counters.RecordEnqueue()
	}
	metrics.ChannelMonitorSuccessfulEnqueues.WithLabelValues(metricsLabel).Inc()

	jobID := ""
	if jobResp != nil {
		jobID = jobResp.ID
	}

	m.log.Info("enqueued youtube_clip.extract job",
		zap.String("video_id", videoID),
		zap.String("title", title),
		zap.Int("segments", len(segments)),
		zap.String("destination_group", category),
		zap.String("job_id", jobID))

	if channel.ID != "" {
		if err := m.channelsSvc.UpdateCursor(ctx, channels.UpdateCursorCommand{
			ID:     channel.ID,
			Cursor: videoID,
		}); err != nil {
			// Cursor update failure is LOGGED ONLY — NOT a rollback
			// trigger. The job already landed in the broker and a
			// second video handling cycle would either resume past
			// the cursor or the next ClaimDue tick would re-scan
			// from the cursor point. Rolling back here would lose
			// the work done by the enqueue tail.
			m.log.Warn("failed to update cursor",
				zap.String("channel_id", channel.ID),
				zap.String("cursor", videoID),
				zap.Error(err))
		}
	}

	return EnqueueOutcome{Enqueued: true, JobID: jobID, Reason: ""}, nil
}
