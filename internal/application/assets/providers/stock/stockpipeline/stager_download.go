package stockpipeline

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	appmetrics "github.com/Marcuss-ops/PipelineGen/internal/application/processmetrics"
)

func (s *StockStager) downloadSource(ctx context.Context, cacheKey string, ref assets.SourceRef, dlReq *SourceDownloadRequest) (*assets.StagedAsset, error) {
	v, sfErr, _ := s.sf.Do(cacheKey, func() (interface{}, error) {
		var result *assets.StagedAsset
		var downloadErr error
		var metric *appmetrics.Handle
		if s.svc != nil && s.svc.metrics != nil {
			jobID, _ := appmetrics.RunIDs(ctx)
			metric = startServiceStockPhase(ctx, s.svc.metrics, "stock.youtube_download", jobID)
			defer func() {
				itemsOut := int64(0)
				bytesOut := int64(0)
				if result != nil {
					itemsOut = 1
					bytesOut = result.Bytes
				}
				metric.SetItems(1, itemsOut)
				metric.SetBytes(0, bytesOut)
				metric.SetDetails(map[string]any{
					"videos_downloaded":   itemsOut,
					"download_bytes":      bytesOut,
					"cache_hit":           false,
					"singleflight_shared": false,
				})
				finishServiceStockPhase(s.svc.log, metric, downloadErr)
			}()
		}

		dlResult, dlErr := s.downloader.Download(ctx, dlReq)
		if dlErr != nil {
			downloadErr = fmt.Errorf("stock stager: yt-dlp download %q: %w", ref.URL, dlErr)
			return nil, downloadErr
		}
		result = &assets.StagedAsset{
			LocalPath: dlResult.ResolvedPath,
			Bytes:     dlResult.SizeBytes,
		}
		s.populateCache(ctx, cacheKey, "youtube", extractVideoIDFromURL(ref.URL), ref, dlResult.ResolvedPath, dlResult.SizeBytes)
		return result, nil
	})
	if sfErr != nil {
		return nil, sfErr
	}
	return v.(*assets.StagedAsset), nil
}
