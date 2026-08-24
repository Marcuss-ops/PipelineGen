// Package monitor — downloader port and video-listing DTOs.
//
// FASE 3.7 (2026-07-04): MonitorDownloaderPort.ListChannelVideos accepts
// a monitor-owned `ListChannelVideosQuery` request shape (was previously
// `downloader.ListChannelVideosRequest`). Zero infra import in this
// file. The composition root
// (`internal/app/lifecycle.go::monitorYtdlpAdapter`) translates
// `monitor.ListChannelVideosQuery` ↔ `downloader.ListChannelVideosRequest`
// at wire-up time, so the application-layer orchestration only sees
// the monitor-canonical types.
package assets

import (
	"context"
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
	ListChannelVideos(ctx context.Context, query ListChannelVideosQuery) ([]VideoInfo, error)
	Path() string
}
