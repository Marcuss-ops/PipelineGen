package monitor

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	"go.uber.org/zap"
)

// checkChannel checks a single channel for new videos.
// PR 4 (June 2026): uses m.ytdlp.ListChannelVideos() (JSON structured output)
// instead of exec.Command + --print text parsing.
// PR 7 (June 2026): uses channels.Channel DTO directly; MonitorConfig removed.
func (m *ChannelMonitor) checkChannel(ctx context.Context, channel channels.Channel) {
	playlistEnd := effectivePlaylistEnd(channel, 50)

	videos, err := m.ytdlp.ListChannel(ctx, channel.ChannelURL, playlistEnd)
	if err != nil {
		m.log.Error("Failed to fetch channel videos", zap.String("url", channel.ChannelURL), zap.Error(err))
		return
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
}
