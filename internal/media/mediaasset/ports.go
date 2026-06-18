package mediaasset

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/pkg/media/downloader"
	"github.com/Marcuss-ops/PipelineGen/pkg/media/ffmpeg"
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
	Probe(ctx context.Context, path string) (*ffmpeg.MediaInfo, error)
	ExtractFrame(ctx context.Context, input, output string, timestamp float64) error
}
