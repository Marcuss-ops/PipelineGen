// Package stockpipeline — downloader_port.go
//
// DownloaderPort is the Pattern 0 typed port for the source
// downloader. StockStager routes source downloads through this port
// so the application layer can be tested with a fake downloader
// that counts invocations and gates operations; the concrete
// *downloader.YTDLPDownloader satisfies DockerPort structurally.
//
// godlike/06 SSOT:
//   - This file is the SOLE owner of the DownloaderPort interface.
//   - The concrete implementation lives in
//     internal/infrastructure/downloader/ytdlp_downloader.go
//     and is injected via WithDownloader at composition time
//     (asset default: NewStockStager constructs the concrete from
//     the composition root; tests override via WithDownloader(fake)).
//
// godlike/07 fail-closed:
//   - When StockStager.s.downloader is nil and a download path is
//     required, StageSource surfaces a typed error
//     (`downloader not wired (cfg nil)`) so the caller can
//     errors.Is probe the wrapper instead of nil-deref'ing.
package types

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
)

// DownloaderPort is the Pattern 0 typed port for the source
// downloader. The single method mirrors *downloader.YTDLPDownloader.Download
// so the concrete type is structural-conformant without an adapter
// wrapper.
//
// Usage in StockStager:
//
//	s.downloader.Download(ctx, &downloader.DownloadRequest{...})
//
// The downloader writes its output to dlReq.OutputPath (or
// OutputPath + ".%(ext)s" if yt-dlp resolves a different container).
// StockStager then resolves the actual downloaded file path via
// downloader.ResolveDownloadedSegmentPath before stat-ing it.
type DownloaderPort interface {
	Download(ctx context.Context, req *downloader.DownloadRequest) error
}
