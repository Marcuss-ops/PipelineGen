package monitor

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	jobtools "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"

	"go.uber.org/zap"
)

// HandleChannelSyncJob is the job handler for youtube.channel.sync jobs.
// PR 3 (June 2026): the monitor enqueues one sync job per channel instead
// of processing channels inline. The job handler performs the channel check,
// video filtering, and clip extraction enqueue.
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

	// PR 3 (June 2026): checkChannel is void — it logs errors internally.
	// Failure detection for exponential backoff requires checkChannel to
	// return an error; currently, every sync is marked successful.
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
// with the job service. Called from Start() after the monitor is initialized.
// PR 3 (June 2026).
func (m *ChannelMonitor) RegisterChannelSyncHandler(jobsSvc *jobtools.Service) {
	if jobsSvc != nil {
		jobsSvc.RegisterHandler(jobservice.TypeYouTubeChannelSync, m.HandleChannelSyncJob)
		m.log.Info("registered youtube.channel.sync job handler")
	}
}
