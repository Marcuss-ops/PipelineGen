package monitor

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"go.uber.org/zap"
)

// checkChannel checks a single channel for new videos.
// PR 4 (June 2026): uses m.ytdlp.ListChannelVideos() (JSON structured output)
// instead of exec.Command + --print text parsing.
// PR 7 (June 2026): uses channels.Channel DTO directly; MonitorConfig removed.
func (m *ChannelMonitor) checkChannel(ctx context.Context, channel channels.Channel) {
	listReq := downloader.ListChannelVideosRequest{ChannelURL: channel.ChannelURL}

	if channel.LookbackDays > 0 {
		sinceDate := time.Now().AddDate(0, 0, -channel.LookbackDays)
		listReq.DateAfter = sinceDate.Format("20060102")
		m.log.Info("lookback mode", zap.String("url", channel.ChannelURL), zap.Int("lookback_days", channel.LookbackDays))
	} else {
		playlistEnd := effectivePlaylistEnd(channel, DefaultPlaylistEnd)
		listReq.PlaylistEnd = playlistEnd
		if playlistEnd == 0 {
			m.log.Info("full scan mode", zap.String("url", channel.ChannelURL))
		}
	}

	videos, err := m.ytdlp.ListChannelVideos(ctx, listReq)
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
