// Package monitor — enqueue.go: durable-job emission + channel cursor update.
//
// Step 9 (June 2026, Channel Monitor Blocco 6 architectural rewrite):
// the package is now exactly 5 production files (scheduler.go,
// discovery.go, analyzer.go, enqueue.go, ports.go). This file owns:
//
//   - enqueueFromAnalysis: builds the EnqueueExtractRequest from the
//     analyzeVideo result, resolves the DriveFolderID (channel-level
//     override + ClipsFolder global fallback), and delegates the actual
//     marshal + jobs.Enqueue + channels.UpdateCursor call to the
//     JobEnqueuer port.
//   - HandleChannelSyncJob: the durable youtube.channel.sync handler.
//     Registered via jobsSvc.RegisterHandler(TypeYouTubeChannelSync, ...)
//     so a queued channel-sync tick goes through this rather than inline.
//   - RegisterChannelSyncHandler: the public binding the lifecycle
//     calls after NewChannelMonitor.
//   - tryReserve: the per-channel budget CAS, only consumed after
//     passing the AI gate (see discovery.go processVideo).
//
// Why a port? Splitting the Enqueue logic out of the analyzer lets the
// analyzer nonchalantly return "no segments → skip" without paying for
// marshal + enqueue. The actual marshaling + jobs.Enqueue + cursor
// update + metrics observation go through m.enqueuer.EnqueueExtract so
// the concrete adapter can own ActiveKey construction + payload shape
// in the cleanest place.
package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"

	"go.uber.org/zap"

	channels "github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	jobtools "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
)

// enqueueFromAnalysis builds the canonical ExtractRequest-shaped payload
// and delegates emission to the JobEnqueuer port.
//
// DriveFolderID resolution rule (preserved verbatim from the pre-Step-9
// process_video.go::enqueueClipExtract):
//   - channel.DriveFolderID if set;
//   - cfg.Drive.ClipsFolder() fallback if channel-level is empty;
//   - empty string is also OK (extraction service will route via
//     category-root + group subfolder as before).
//
// The metric labels are read off the channel handle (via
// extractChannelHandle, which lives in scheduler.go); an empty handle
// degrades to "unknown" so the Prometheus series never sees a blank label.
//
// Errors from the JobEnqueuer port are logged and swallowed: the
// channel-monitor's contract is best-effort per video, with retry
// driven by the next scheduler tick (cursor advancement is gated on
// the enqueue success, so a non-enqueued video gets re-discovered on
// the next run unless the user adds a "MaxVideosPerRun consume on
// enqueue-only" refinement in Blocco 7).
func (m *ChannelMonitor) enqueueFromAnalysis(ctx context.Context, info downloader.VideoInfo, channel channels.Channel, analysis Analysis) {
	videoID := info.ID
	title := info.Title
	videoURL := fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)

	// ── Drive folder resolution ──────────────────────────────────────
	driveFolderID := channel.DriveFolderID
	if driveFolderID == "" && m.cfg != nil {
		driveFolderID = m.cfg.Drive.ClipsFolder()
	}

	// ── Log prefix ───────────────────────────────────────────────────
	channelHandle := extractChannelHandle(channel.ChannelURL)
	if channelHandle == "" {
		channelHandle = "unknown"
	}
	m.log.Debug("enqueueFromAnalysis: dispatching via JobEnqueuer",
		zap.String("video_id", videoID),
		zap.String("channel_handle", channelHandle),
		zap.Int("segments", len(analysis.Segments)))

	// ── Delegate to the JobEnqueuer port ─────────────────────────────
	if m.enqueuer == nil {
		m.log.Warn("enqueueFromAnalysis: enqueuer port not wired, cannot emit extract job",
			zap.String("video_id", videoID))
		return
	}
	if emitErr := m.enqueuer.EnqueueExtract(ctx, EnqueueExtractRequest{
		VideoID:       videoID,
		Title:         title,
		URL:           videoURL,
		Group:         analysis.Category,
		DriveFolderID: driveFolderID,
		Segments:      analysis.Segments,
		Channel:       channel,
	}); emitErr != nil {
		m.log.Error("enqueueFromAnalysis: JobEnqueuer.EnqueueExtract failed",
			zap.String("video_id", videoID),
			zap.Error(emitErr))
		return
	}

	m.log.Info("enqueued youtube_clip.extract job",
		zap.String("video_id", videoID),
		zap.String("title", title),
		zap.Int("segments", len(analysis.Segments)),
		zap.String("destination_group", analysis.Category))
}

// HandleChannelSyncJob is the durable youtube.channel.sync handler.
//
// PR 3 (June 2026): the monitor enqueues one sync job per channel instead
// of processing channels inline. The job handler performs the channel
// check, video filtering, and clip extraction enqueue.
//
// Per Blocco 1 (channel-monitor hardening): the handler invokes
// checkChannel and unconditionally writes MarkChecked(Success=true) —
// the recovery from a sync-failure-driven backoff path is the
// scheduler's responsibility (the next runSchedulerCycle will re-claim
// this channel when NextCheckAt fires; backoff is preserved on
// scheduler-level failures, not on sync-internal policy rejects).
func (m *ChannelMonitor) HandleChannelSyncJob(ctx context.Context, j *jobservice.Job, tools *jobtools.JobTools) (map[string]any, error) {
	var payload struct {
		ChannelID string `json:"channel_id"`
	}
	if len(j.Payload) > 0 {
		if err := json.Unmarshal(j.Payload, &payload); err != nil {
			return nil, fmt.Errorf("channel_sync: invalid payload: %w", err)
		}
	}
	if payload.ChannelID == "" {
		return nil, fmt.Errorf("channel_sync: missing channel_id in payload")
	}

	ch, err := m.channelsSvc.GetByID(ctx, payload.ChannelID)
	if err != nil {
		return nil, fmt.Errorf("channel_sync: channel lookup failed for %q: %w", payload.ChannelID, err)
	}

	m.log.Info("handling youtube.channel.sync job",
		zap.String("job_id", j.ID),
		zap.String("channel_id", payload.ChannelID),
		zap.String("channel_url", ch.ChannelURL))

	// Defensive panic-recover so a bad channel list does not poison the
	// whole job worker pool (the dispatcher's contract: never panic back).
	func() {
		defer func() {
			if r := recover(); r != nil {
				m.log.Error("panic in channel sync job",
					zap.String("channel_id", payload.ChannelID),
					zap.Any("recover", r))
			}
		}()
		m.checkChannel(ctx, ch)
	}()

	// Mark checked on success — the next check time uses the channel's normal interval.
	nextCheckAt := m.nextCheckTime(ch, true)
	if err := m.channelsSvc.MarkChecked(ctx, channels.MarkCheckedCommand{
		ID:          ch.ID,
		NextCheckAt: nextCheckAt,
		Success:     true,
	}); err != nil {
		m.log.Error("failed to mark channel checked after sync",
			zap.String("channel_id", ch.ID),
			zap.Error(err))
	}

	return map[string]any{
		"channel_id": payload.ChannelID,
		"status":     "synced",
	}, nil
}

// RegisterChannelSyncHandler registers the youtube.channel.sync job handler
// with the job service. Called from the lifecycle after the monitor is
// initialized (PR 3, June 2026).
func (m *ChannelMonitor) RegisterChannelSyncHandler(jobsSvc *jobtools.Service) {
	if jobsSvc == nil || m == nil {
		return
	}
	jobsSvc.RegisterHandler(jobservice.TypeYouTubeChannelSync, m.HandleChannelSyncJob)
	m.log.Info("registered youtube.channel.sync job handler")
}

// tryReserve is the per-channel budget check (atomic CAS). It is
// consumed only AFTER the AI gate (see discovery.go::processVideo) so
// a transient transcript miss does not waste a budget slot.
func tryReserve(counter *atomic.Int32, limit int) bool {
	for {
		current := counter.Load()
		if current >= int32(limit) {
			return false
		}
		if counter.CompareAndSwap(current, current+1) {
			return true
		}
	}
}
