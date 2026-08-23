package processor

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	downloader "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
)

type YTDLP interface {
	Download(ctx context.Context, req *downloader.DownloadRequest) error
}

type HTTPDownloader interface {
	Download(ctx context.Context, req *downloader.HTTPDownloadRequest) error
}

type VideoProcessor interface {
	Normalize(ctx context.Context, inputPath, outputPath string, opts mediaexec.NormalizeOptions) error
	RemuxHLS(ctx context.Context, sourceURL, outputPath string) error
	Probe(ctx context.Context, path string) (*mediaexec.MediaInfo, error)
	ExtractFrame(ctx context.Context, input, output string, timestamp float64) error
	GenerateProxy(ctx context.Context, input, output string) error
	GenerateStoryboard(ctx context.Context, input, output string, intervalFrames, cols, rows int) error
}
