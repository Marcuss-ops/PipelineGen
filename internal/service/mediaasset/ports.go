package mediaasset

import (
	"context"

	"velox/go-master/pkg/media/downloader"
	"velox/go-master/pkg/media/ffmpeg"
)

type YTDLP interface {
	Download(ctx context.Context, req *downloader.DownloadRequest) error
}

type HTTPDownloader interface {
	Download(ctx context.Context, req *downloader.HTTPDownloadRequest) error
}

type VideoProcessor interface {
	Normalize(ctx context.Context, inputPath, outputPath string, opts ffmpeg.NormalizeOptions) error
	RemuxHLS(ctx context.Context, sourceURL, outputPath string) error
}
