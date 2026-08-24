// Package downloader — stock_adapter.go (PR-REFACTOR-P0-IO-BINDER, July 2026).
//
// StockDownloaderAdapter adapts the concrete *YTDLPDownloader to the
// application-layer stockpipeline.SourceDownloader port. It translates
// SourceDownloadRequest → DownloadRequest, calls the real downloader,
// resolves the yt-dlp output template, and returns DownloadedSource.
//
// The adapter also satisfies stockpipeline.ChannelLister by forwarding
// ListChannel calls and translating the infra VideoInfo to the
// app-layer VideoInfo DTO.
//
// godlike/06 SSOT: this file is the SOLE bridge between the stockpipeline
// application ports and the concrete YTDLPDownloader. The composition
// root constructs it and injects it via WithDownloader / as ChannelLister.
package downloader

import (
	"context"
	"fmt"
	"os"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
)

// StockDownloaderAdapter adapts *YTDLPDownloader to
// stockpipeline.SourceDownloader + stockpipeline.ChannelLister.
type StockDownloaderAdapter struct {
	inner *YTDLPDownloader
}

// NewStockAdapter creates an adapter that bridges the concrete
// YTDLPDownloader to the stockpipeline application ports.
func NewStockAdapter(inner *YTDLPDownloader) *StockDownloaderAdapter {
	return &StockDownloaderAdapter{inner: inner}
}

// Download translates SourceDownloadRequest → DownloadRequest,
// calls the inner downloader, resolves the output path, and
// returns DownloadedSource with the resolved path and file size.
func (a *StockDownloaderAdapter) Download(ctx context.Context, req *stockpipeline.SourceDownloadRequest) (*stockpipeline.DownloadedSource, error) {
	dlReq := &DownloadRequest{
		URL:              req.URL,
		OutputPath:       req.OutputPath,
		DownloadSections: req.DownloadSections,
		ForceKeyframes:   req.ForceKeyframes,
		MergeFormat:      req.MergeFormat,
		NoPlaylist:       req.NoPlaylist,
		UseCookies:       req.UseCookies,
	}

	if err := a.inner.Download(ctx, dlReq); err != nil {
		return nil, fmt.Errorf("stock downloader adapter: %w", err)
	}

	// Resolve the actual downloaded file path from yt-dlp's output template.
	resolved, resolveErr := ResolveDownloadedSegmentPath(req.OutputPath + ".%(ext)s")
	if resolveErr != nil {
		return nil, fmt.Errorf("stock downloader adapter: resolve output: %w", resolveErr)
	}

	fi, statErr := os.Stat(resolved)
	if statErr != nil {
		return nil, fmt.Errorf("stock downloader adapter: stat %q: %w", resolved, statErr)
	}

	return &stockpipeline.DownloadedSource{
		ResolvedPath: resolved,
		SizeBytes:    fi.Size(),
	}, nil
}

// ListChannel forwards to the inner downloader and translates
// infra VideoInfo → app-layer VideoInfo DTO.
func (a *StockDownloaderAdapter) ListChannel(ctx context.Context, channelURL string, limit int) ([]stockpipeline.VideoInfo, error) {
	raw, err := a.inner.ListChannel(ctx, channelURL, limit)
	if err != nil {
		return nil, err
	}
	out := make([]stockpipeline.VideoInfo, len(raw))
	for i, v := range raw {
		out[i] = stockpipeline.VideoInfo{
			ID:       v.ID,
			Title:    v.Title,
			Duration: v.Duration,
		}
	}
	return out, nil
}

// Compile-time assertions: StockDownloaderAdapter satisfies both ports.
var _ stockpipeline.SourceDownloader = (*StockDownloaderAdapter)(nil)
var _ stockpipeline.ChannelLister = (*StockDownloaderAdapter)(nil)
