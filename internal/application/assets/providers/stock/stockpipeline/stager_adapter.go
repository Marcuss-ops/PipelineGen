// Package stockpipeline — stager_adapter.go (Step 9/12, July 2026).
//
// StockStager wraps stockpipeline.Service.StageSource behind the
// canonical assets.SourceStager port so callers can stage stock
// source media without depending on the full stockpipeline.Service.
package stockpipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
)

// Compile-time assertion: *StockStager satisfies assets.SourceStager.
var _ assets.SourceStager = (*StockStager)(nil)

// StockStager adapts a stockpipeline.Service (or any stockRunner) to
// the shared assets.SourceStager port. It delegates to the service's
// existing StageSource method.
type StockStager struct {
	svc *Service
}

// NewStockStager wraps a stockpipeline.Service as an assets.SourceStager.
// svc must be non-nil; nil produces a runtime error on StageSource.
func NewStockStager(svc *Service) *StockStager {
	return &StockStager{svc: svc}
}

// StageSource implements assets.SourceStager. When DownloadSection is
// set, it downloads a time-slice of the video via yt-dlp's
// --download-sections. Otherwise it delegates to the service's
// StageSource method for a full-asset download.
func (s *StockStager) StageSource(ctx context.Context, ref assets.SourceRef) (*assets.StagedAsset, error) {
	if s.svc == nil {
		return nil, fmt.Errorf("stock stagervc: service not wired")
	}
	if ref.URL == "" {
		return nil, fmt.Errorf("stock stagervc: empty URL")
	}

	// Non-section path: delegate to existing StageSource.
	if ref.DownloadSection == "" {
		staged, err := s.svc.StageSource(ctx, ref.URL)
		if err != nil {
			return nil, fmt.Errorf("stock stagervc: stage source %q: %w", ref.URL, err)
		}
		return &assets.StagedAsset{
			LocalPath: staged.LocalPath,
			Bytes:     staged.Bytes,
		}, nil
	}

	// Section path: download via yt-dlp directly (same path
	// processSingleVideo uses).
	return s.svc.stageSection(ctx, ref)
}

// Cleanup removes the staged file's parent temp directory.
func (s *StockStager) Cleanup(ctx context.Context, staged *assets.StagedAsset) error {
	if staged == nil || staged.LocalPath == "" {
		return nil
	}
	dir := filepath.Dir(staged.LocalPath)
	if dir == "" || dir == "." || dir == "/" {
		return nil
	}
	return os.RemoveAll(dir)
}
