package stockpipeline

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	appmetrics "github.com/Marcuss-ops/PipelineGen/internal/application/processmetrics"
)

func (s *StockStager) downloadSource(ctx context.Context, cacheKey string, ref assets.SourceRef, dlReq *SourceDownloadRequest) (result *assets.StagedAsset, err error) {
	metric := (*appmetrics.Handle)(nil)
	if s != nil && s.svc != nil {
		jobID, _ := appmetrics.RunIDs(ctx)
		metric = startServiceStockPhase(ctx, s.svc.metrics, "stock.youtube_download", jobID)
	}
	defer func() {
		if metric != nil {
			if result != nil {
				metric.SetItems(1, 1)
				metric.SetBytes(0, result.Bytes)
				metric.SetDetails(map[string]any{
					"videos_downloaded": 1,
					"download_bytes":    result.Bytes,
				})
			}
			finishServiceStockPhase(s.svc.log, metric, err)
		}
	}()
	v, sfErr, _ := s.sf.Do(cacheKey, func() (interface{}, error) {
		dlResult, dlErr := s.downloader.Download(ctx, dlReq)
		if dlErr != nil {
			return nil, fmt.Errorf("stock stager: yt-dlp download %q: %w", ref.URL, dlErr)
		}
		s.populateCache(ctx, cacheKey, "youtube", extractVideoIDFromURL(ref.URL), ref, dlResult.ResolvedPath, dlResult.SizeBytes)
		return &assets.StagedAsset{
			LocalPath: dlResult.ResolvedPath,
			Bytes:     dlResult.SizeBytes,
		}, nil
	})
	if sfErr != nil {
		return nil, sfErr
	}
	return v.(*assets.StagedAsset), nil
}
