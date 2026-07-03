// Package monitor — downloader port and video-listing DTOs.
package monitor

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
)

// VideoInfo is the monitor-owned projection of a YouTube video's listing metadata.
type VideoInfo struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	Views    int64   `json:"view_count"`
	Duration float64 `json:"duration"`
}

// MonitorDownloaderPort is the minimum yt-dlp surface required by the channel monitor.
type MonitorDownloaderPort interface {
	ListChannelVideos(ctx context.Context, req downloader.ListChannelVideosRequest) ([]VideoInfo, error)
	Path() string
}
