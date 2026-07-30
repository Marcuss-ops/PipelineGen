package stockpipeline

import (
	"context"
	"fmt"

	appassets "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
)

// serviceSourceDownloader bridges the canonical acquisition.SourceStager
// already wired by the composition root into StockStager's download port.
// Keeping this adapter here prevents stagerForRun from constructing an
// unconfigured StockStager and guarantees section requests retain their
// DownloadSection all the way to the acquisition adapter.
type serviceSourceDownloader struct {
	service *Service
}

func (d serviceSourceDownloader) Download(ctx context.Context, req *SourceDownloadRequest) (*DownloadedSource, error) {
	if d.service == nil || req == nil || req.URL == "" {
		return nil, fmt.Errorf("stock service source downloader: invalid request")
	}

	var staged *appassets.StagedAsset
	var err error
	if len(req.DownloadSections) > 0 {
		if len(req.DownloadSections) != 1 {
			return nil, fmt.Errorf("stock service source downloader: expected one section, got %d", len(req.DownloadSections))
		}
		staged, err = d.service.stageSection(ctx, appassets.SourceRef{
			URL:             req.URL,
			DownloadSection: req.DownloadSections[0],
			ForceKeyframes:  req.ForceKeyframes,
			MergeFormat:     req.MergeFormat,
		})
	} else {
		var full *StagedSource
		full, err = d.service.StageSource(ctx, req.URL)
		if full != nil {
			staged = &appassets.StagedAsset{LocalPath: full.LocalPath, Bytes: full.Bytes}
		}
	}
	if err != nil {
		return nil, err
	}
	if staged == nil || staged.LocalPath == "" || staged.Bytes <= 0 {
		return nil, fmt.Errorf("stock service source downloader: empty staged result")
	}
	return &DownloadedSource{ResolvedPath: staged.LocalPath, SizeBytes: staged.Bytes}, nil
}
