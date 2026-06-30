package monitor

import (
	"context"
	"fmt"
	"sync"

	"github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	"go.uber.org/zap"
)

// checkChannel checks a single channel for new videos.
//
// PR 4 (June 2026): uses m.ytdlp.ListChannel() (JSON structured output)
// instead of exec.Command + --print text parsing.
// PR 7 (June 2026): uses channels.Channel DTO directly; MonitorConfig removed.
// PR (June 2026, Blocco 1 of channel-monitor hardening): now returns
// (ChannelCheckResult, error) instead of bare `()`. The scheduler
// feeds `error` straight into nextCheckTime so a yt-dlp failure drives
// the exponential backoff curve (5min → 10min → … → 24h) for real, no
// longer collapsed to the always-success path.
// PR (June 2026, Blocco 4 Step 2): the single `acceptedCount atomic.Int32`
// is replaced by `var counters ChannelCounters` so the two halves of
// the budget semantics — analyses reserved vs jobs successfully
// enqueued — are observable independently. Step 2 keeps them in
// lockstep (parity with the prior single-counter behaviour); Step 3
// will split the lockstep and surface the divergence.
//
// Err is non-nil ONLY when the yt-dlp structured listing itself failed
// (network, parse, subprocess error, etc.). In-process filter rejections
// (below min_views, title-keyword miss, semantic budget exhaustion,
// semantic score below threshold) count toward VideosSkipped and
// produce a nil error: the check itself succeeded; those rejections
// are policy, not infra. Conflating them would trigger a backoff
// after every policy rejection, which is wrong.
//
// VideosSkipped = VideosDiscovered - AnalysisReservations (renamed
// from acceptedCount in Step 2): covers early loop breakouts
// (MaxVideosPerRun reached), in-flight MaxVideosPerRun rejections,
// and processVideo's filter chain (min_views / duration / title
// keyword / semantic budget / semantic score) — all of which bump
// AnalysisReservations by 0 from the caller's perspective.
//
// **Soft-signal caveat**: in Step 2 AnalysisReservations ==
// SuccessfulEnqueues (lockstep, parity with the previous behaviour).
// VideosSuccessfulEnqueues reads counters.SuccessfulEnqueues.Load(),
// which counts *tryReserve successes* (the per-channel MaxVideosPerRun
// budget slot consumption), not the jobs actually posted to the
// broker. If processVideo's tail (enqueueClipExtract) later rejects
// internally — nil jobsSvc, zero interesting-segments, marshal
// failure, jobs.Enqueue error, ActiveKey collision — the slot
// has been consumed but no job was posted. Operators should treat
// VideosSuccessfulEnqueues as a *"videos passed through the
// per-channel filter chain and were permitted to enter the
// extraction pipeline"* signal, not a hard "jobs accepted by the
// broker" count. Step 3 will split the lockstep and tighten the
// contract so enqueueClipExtract's tail success path increments
// SuccessfulEnqueues independently AND rolls back AnalysisReservations
// on failure.
func (m *ChannelMonitor) checkChannel(ctx context.Context, channel channels.Channel) (ChannelCheckResult, error) {
	playlistEnd := effectivePlaylistEnd(channel, 50)

	videos, err := m.ytdlp.ListChannel(ctx, channel.ChannelURL, playlistEnd)
	if err != nil {
		m.log.Error("Failed to fetch channel videos", zap.String("url", channel.ChannelURL), zap.Error(err))
		return ChannelCheckResult{}, fmt.Errorf("list channel %q: %w", channel.ChannelURL, err)
	}

	m.log.Info("Fetched channel videos", zap.String("url", channel.ChannelURL), zap.Int("count", len(videos)))

	concurrency := 5
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var counters ChannelCounters

	for _, video := range videos {
		video := video
		if channel.MaxVideosPerRun > 0 && counters.AnalysisReservations.Load() >= int32(channel.MaxVideosPerRun) {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if r := recover(); r != nil {
				m.log.Error("panic in video processing worker", zap.Any("recover", r), zap.String("video_id", video.ID))
			}
			if channel.MaxVideosPerRun > 0 && counters.AnalysisReservations.Load() >= int32(channel.MaxVideosPerRun) {
				return
			}
			m.processVideo(ctx, video, channel, &counters)
		}()
	}
	wg.Wait()

	reservations := counters.Reservations()
	enqueued := counters.Enqueued()
	return ChannelCheckResult{
		VideosDiscovered:           len(videos),
		VideosAnalysisReservations: reservations,
		VideosSuccessfulEnqueues:   enqueued,
		VideosEnqueued:             enqueued,
		VideosSkipped:              len(videos) - reservations,
	}, nil
}
