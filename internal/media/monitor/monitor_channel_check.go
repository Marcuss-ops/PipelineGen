package monitor

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// checkChannel checks a single channel for new videos.
func (m *ChannelMonitor) checkChannel(ctx context.Context, channel ChannelConfig, cfg *MonitorConfig) {
	var dateAfter string
	lookbackMode := channel.LookbackDays > 0
	if lookbackMode {
		sinceDate := time.Now().AddDate(0, 0, -channel.LookbackDays)
		dateAfter = sinceDate.Format("20060102")
		m.log.Info("lookback mode: scanning videos since", zap.String("url", channel.URL), zap.Int("lookback_days", channel.LookbackDays), zap.String("date_after", dateAfter))
	} else {
		playlistEnd := effectivePlaylistEnd(channel, cfg.PlaylistEnd)
		if playlistEnd == 0 {
			m.log.Info("full scan mode: fetching ALL videos from channel", zap.String("url", channel.URL))
		} else {
			m.log.Debug("scanning recent videos", zap.String("url", channel.URL), zap.Int("playlist_end", playlistEnd))
		}
	}

	args := []string{
		"--flat-playlist",
		"--print", "%(id)s %(title)s %(view_count)s %(duration)s",
	}

	if lookbackMode {
		args = append(args, "--dateafter", dateAfter)
	} else {
		playlistEnd := effectivePlaylistEnd(channel, cfg.PlaylistEnd)
		args = append(args, "--playlist-end", fmt.Sprintf("%d", playlistEnd))
	}

	if cfg.CookiesPath != "" {
		args = append(args, "--cookies", cfg.CookiesPath)
	}

	args = append(args, channel.URL)

	cmd := exec.Command(cfg.YtdlpPath, args...)
	output, err := cmd.Output()
	if err != nil {
		m.log.Error("Failed to fetch channel videos", zap.String("url", channel.URL), zap.Error(err))
		return
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	concurrency := 5
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var acceptedCount atomic.Int32

	for _, line := range lines {
		line := line
		if channel.MaxVideosPerRun > 0 && acceptedCount.Load() >= int32(channel.MaxVideosPerRun) {
			break
		}

		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					m.log.Error("panic in video processing worker", zap.Any("recover", r), zap.String("line", line))
				}
			}()
			defer wg.Done()
			defer func() { <-sem }()

			if channel.MaxVideosPerRun > 0 && acceptedCount.Load() >= int32(channel.MaxVideosPerRun) {
				return
			}

			m.processVideoLine(ctx, line, channel, cfg, &acceptedCount)
		}()
	}

	wg.Wait()
}

// ensureChannelFolders validates and creates per-channel subfolders on Drive.
func (m *ChannelMonitor) ensureChannelFolders(ctx context.Context, channels []ChannelConfig) {
	if m.youtubeSvc == nil {
		return
	}
	for _, ch := range channels {
		if ch.DriveFolderID == "" {
			m.log.Warn("channel has no drive_folder_id, skipping", zap.String("url", ch.URL))
			continue
		}
		channelHandle := extractChannelHandle(ch.URL)
		if channelHandle == "" {
			m.log.Warn("could not extract channel handle from URL", zap.String("url", ch.URL))
			continue
		}
		folderID, err := m.youtubeSvc.GetOrCreateChannelFolder(ctx, channelHandle, ch.DriveFolderID)
		if err != nil {
			m.log.Warn("failed to ensure channel folder on Drive", zap.String("channel", channelHandle), zap.Error(err))
		} else {
			m.log.Info("channel folder ready on Drive", zap.String("channel", channelHandle), zap.String("folder_id", folderID))
		}
	}
}
