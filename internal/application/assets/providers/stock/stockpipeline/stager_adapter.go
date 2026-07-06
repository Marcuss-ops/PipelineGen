// Package stockpipeline — stager_adapter.go (Step 9/12, July 2026).
//
// StockStager wraps stockpipeline.Service.StageSource behind the
// canonical assets.SourceStager port so callers can stage stock
// source media without depending on the full stockpipeline.Service.
//
// July 2026 (DIRECT-YTDLP): StockStager downloads directly via
// yt-dlp instead of routing through Service.StageSource →
// acquisition.SourceStager.Prepare. The acquisition chain causes
// nil-deref when sourceStager is not wired at composition root;
// the yt-dlp direct path is the production-tested download path.
package stockpipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
)

// Compile-time assertion: *StockStager satisfies assets.SourceStager.
var _ assets.SourceStager = (*StockStager)(nil)

// StockStager adapts a stockpipeline.Service to the shared
// assets.SourceStager port. It downloads directly via yt-dlp,
// bypassing the acquisition.SourceStager chain.
type StockStager struct {
	svc   *Service
	ytdlp *downloader.YTDLPDownloader
}

// NewStockStager wraps a stockpipeline.Service as an assets.SourceStager.
// svc must be non-nil; nil produces a runtime error on StageSource.
// The yt-dlp downloader is constructed from the service's config.
func NewStockStager(svc *Service) *StockStager {
	var ytdlp *downloader.YTDLPDownloader
	if svc != nil && svc.cfg != nil {
		ytdlp = downloader.NewYTDLP(svc.cfg)
	}
	return &StockStager{svc: svc, ytdlp: ytdlp}
}

// StageSource implements assets.SourceStager. Downloads the source video
// directly via yt-dlp (bypassing the acquisition.SourceStager chain).
func (s *StockStager) StageSource(ctx context.Context, ref assets.SourceRef) (*assets.StagedAsset, error) {
	if s.svc == nil {
		return nil, fmt.Errorf("stock stager: service not wired")
	}
	if ref.URL == "" {
		return nil, fmt.Errorf("stock stager: empty URL")
	}
	if s.ytdlp == nil {
		return nil, fmt.Errorf("stock stager: yt-dlp downloader not wired (cfg nil)")
	}

	// Create a temp staging directory under the service's temp path.
	tmpDir, err := os.MkdirTemp(s.svc.cfg.Storage.TempPath(), "stock_stage_")
	if err != nil {
		return nil, fmt.Errorf("stock stager: create temp dir: %w", err)
	}

	outputPath := filepath.Join(tmpDir, "source.mp4")

	dlReq := &downloader.DownloadRequest{
		URL:        ref.URL,
		OutputPath: outputPath,
		NoPlaylist: true,
		UseCookies: false,
	}
	if ref.DownloadSection != "" {
		dlReq.DownloadSections = []string{ref.DownloadSection}
		dlReq.ForceKeyframes = ref.ForceKeyframes
	}
	if ref.MergeFormat != "" {
		dlReq.MergeFormat = ref.MergeFormat
	}

	if err := s.ytdlp.Download(ctx, dlReq); err != nil {
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("stock stager: yt-dlp download %q: %w", ref.URL, err)
	}

	// Resolve the actual downloaded file path.
	resolved, resolveErr := downloader.ResolveDownloadedSegmentPath(outputPath + ".%(ext)s")
	if resolveErr != nil {
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("stock stager: resolve downloaded file: %w", resolveErr)
	}

	fi, statErr := os.Stat(resolved)
	if statErr != nil {
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("stock stager: stat %q: %w", resolved, statErr)
	}

	return &assets.StagedAsset{
		LocalPath: resolved,
		Bytes:     fi.Size(),
	}, nil
}

// Cleanup removes the staged file's parent temp directory.
func (s *StockStager) Cleanup(_ context.Context, staged *assets.StagedAsset) error {
	if staged == nil || staged.LocalPath == "" {
		return nil
	}
	dir := filepath.Dir(staged.LocalPath)
	if dir == "" || dir == "." || dir == "/" {
		return nil
	}
	return os.RemoveAll(dir)
}
