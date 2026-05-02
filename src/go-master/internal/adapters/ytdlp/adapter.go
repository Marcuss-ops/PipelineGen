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
