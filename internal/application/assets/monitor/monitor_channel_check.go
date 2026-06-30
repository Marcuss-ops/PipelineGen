package monitor

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

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
//
// Err is non-nil ONLY when the yt-dlp structured listing itself failed
// (network, parse, subprocess error, etc.). In-process filter rejections
// (below min_views, title-keyword miss, semantic budget exhaustion,
// semantic score below threshold) count toward VideosSkipped and
// produce a nil error: the check itself succeeded; those rejections
// are policy, not infra. Conflating them would trigger a backoff
// after every policy rejection, which is wrong.
//
// VideosSkipped = VideosDiscovered - VideosEnqueued: covers early
// loop breakouts (MaxVideosPerRun reached), in-flight MaxVideosPerRun
// rejections, and processVideo's filter chain (min_views / duration /
// title keyword / semantic budget / semantic score) — all of which
// bump acceptedCount by 0 from the caller's perspective.
//
// **Soft-signal caveat**: VideosEnqueued reads processVideo's
// acceptedCount.Load(), which counts *tryReserve successes* (the
// per-channel MaxVideosPerRun budget slot consumption), not the
// jobs actually posted to the broker. If processVideo's tail
// (enqueueClipExtract) later rejects internally — nil jobsSvc, zero
// interesting-segments, marshal failure, jobs.Enqueue error,
// ActiveKey collision — the slot has been consumed but no job was
// posted. Operators should treat VideosEnqueued as a *"videos
// passed through the per-channel filter chain and were permitted
// to enter the extraction pipeline"* signal, not a hard "jobs
// accepted by the broker" count. Tightening the contract to wire
// enqueueClipExtract's tail success back into acceptedCount is
// tracked for Blocco 2 (semantic path unification).
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
	var acceptedCount atomic.Int32

	for _, video := range videos {
		video := video
		if channel.MaxVideosPerRun > 0 && acceptedCount.Load() >= int32(channel.MaxVideosPerRun) {
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
			if channel.MaxVideosPerRun > 0 && acceptedCount.Load() >= int32(channel.MaxVideosPerRun) {
				return
			}
			m.processVideo(ctx, video, channel, &acceptedCount)
		}()
	}
	wg.Wait()

	enqueued := int(acceptedCount.Load())
	return ChannelCheckResult{
		VideosDiscovered: len(videos),
		VideosEnqueued:   enqueued,
		VideosSkipped:    len(videos) - enqueued,
	}, nil
}
