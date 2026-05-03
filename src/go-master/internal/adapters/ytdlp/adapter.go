// Package ytdlp defines adapter interfaces for yt-dlp download operations.
//
// STATUS: EXPERIMENTAL - Interface defined but not yet implemented or used.
// TODO: Implement and migrate download operations to use this adapter.
package ytdlp

import "context"

type DownloadInput struct {
	URL       string
	OutputDir string
	Filename  string
	StartTime *string
	EndTime   *string
}

type DownloadResult struct {
	LocalPath string
	Title     string
	Duration  float64
}

type YTDLPAdapter interface {
	Download(ctx context.Context, input DownloadInput) (*DownloadResult, error)
}
