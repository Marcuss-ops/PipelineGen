package stockpipeline

import (
	"context"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

func (s *StockStager) downloadSource(ctx context.Context, cacheKey string, ref assets.SourceRef, req *SourceDownloadRequest) (*assets.StagedAsset, error) {
	v, sfErr, _ := s.sf.Do(cacheKey, func() (interface{}, error) {
		var result *assets.StagedAsset
		var downloadErr error
		h := (*kernobs.StageHandle)(nil)
		if s.svc != nil {
			h = startServiceStockPhase(ctx, "stock.youtube_download", "")
			defer func() {
				out, bytes := int64(0), int64(0)
				if result != nil {
					out, bytes = 1, result.Bytes
				}
				h.SetItems(1, out)
				h.SetBytes(0, bytes)
				h.SetItemsFailed(boolToInt64(downloadErr != nil))
				finishServiceStockPhase(s.svc.log, h, downloadErr)
			}()
		}
		dlResult, err := s.downloader.Download(ctx, req)
		if err != nil {
			downloadErr = fmt.Errorf("stock stager: yt-dlp download %q: %w", ref.URL, err)
			return nil, downloadErr
		}
		result = &assets.StagedAsset{LocalPath: dlResult.ResolvedPath, Bytes: dlResult.SizeBytes}
		s.populateCache(ctx, cacheKey, "youtube", extractVideoIDFromURL(ref.URL), ref, dlResult.ResolvedPath, dlResult.SizeBytes)
		return result, nil
	})
	if sfErr != nil {
		return nil, sfErr
	}
	return v.(*assets.StagedAsset), nil
}
