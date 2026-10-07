// Package app contains composition-root wiring for source acquisition.
package wiring

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"

	appacq "github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
	infacq "github.com/Marcuss-ops/PipelineGen/internal/platform/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/downloader"
)

// ErrStockPipelineStagerInit identifies a composition-time failure while
// constructing the canonical acquisition stager.
var ErrStockPipelineStagerInit = errors.New("internal/app: acquisition stager initialization failed")

// WireAcquisitionStager constructs the canonical filesystem-backed stager
// used by the stock pipeline. Unwired fetching remains fail-closed: callers
// receive ErrAcquisitionPrepareFailed rather than a successful no-op.
func WireAcquisitionStager(cfg *config.Config, log *zap.Logger, fetch infacq.FetchFn) (appacq.SourceStager, error) {
	if cfg == nil {
		return nil, fmt.Errorf("%w: cfg is nil", ErrStockPipelineStagerInit)
	}
	if log == nil {
		log = zap.NewNop()
	}
	if fetch == nil {
		fetch = func(_ context.Context, _ appacq.PrepareRequest, _ string, _ func(string)) error {
			return appacq.Wrap(appacq.ErrAcquisitionPrepareFailed, "acquisition fetch is not wired")
		}
	}

	stager, err := infacq.NewFilesystemStager(infacq.Options{
		StagingRoot: filepath.Join(cfg.Storage.TempPath(), "stock_pipeline_staging"),
		Fetch:       fetch,
		Log:         log,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrStockPipelineStagerInit, err)
	}
	return stager, nil
}

// buildYouTubeSourceStager wires the sections-only acquisition SourceStager
// used by the YouTube fanout: the fetcher downloads ONE contiguous block with
// a single yt-dlp --download-sections call (and a single cookie-authenticated
// invocation) instead of the whole source. Fail-soft by design: a staging
// error only logs — the fanout keeps the per-segment yt-dlp path (backwards
// compatible).
func buildYouTubeSourceStager(cfg *config.Config, log *zap.Logger) appacq.SourceStager {
	var youtubeSourceStager appacq.SourceStager
	ytdlpDL := downloader.NewYTDLP(cfg)
	fetch := func(ctx context.Context, req appacq.PrepareRequest, dstPath string, _ func(string)) error {
		dlReq := &downloader.DownloadRequest{
			URL:        req.Source.URL,
			OutputPath: dstPath + ".%(ext)s",
			Timeout:    req.Timeout,
			UseCookies: true,
		}
		if req.Source.MergeFormat != "" {
			dlReq.MergeFormat = req.Source.MergeFormat
		} else {
			dlReq.MergeFormat = "mp4"
		}
		if req.Source.DownloadSection != "" {
			dlReq.DownloadSections = []string{req.Source.DownloadSection}
			dlReq.ForceKeyframes = req.Source.ForceKeyframes
		}
		if err := ytdlpDL.Download(ctx, dlReq); err != nil {
			return err
		}
		tmpl := dstPath + ".%(ext)s"
		resolved, rErr := downloader.ResolveDownloadedSegmentPath(tmpl)
		if rErr != nil {
			return rErr
		}
		if resolved != dstPath {
			if err := os.Rename(resolved, dstPath); err != nil {
				return err
			}
		}
		return nil
	}
	if s, sErr := WireAcquisitionStager(cfg, log, fetch); sErr != nil {
		log.Warn("youtube section stager unavailable; fanout will use per-segment yt-dlp", zap.Error(sErr))
	} else {
		youtubeSourceStager = s
		log.Info("youtube section SourceStager wired", zap.String("staging_root", filepath.Join(cfg.Storage.TempPath(), "stock_pipeline_staging")))
	}
	return youtubeSourceStager
}
