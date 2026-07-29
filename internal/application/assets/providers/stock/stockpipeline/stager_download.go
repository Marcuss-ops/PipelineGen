package stockpipeline

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
)

func (s *StockStager) downloadSource(ctx context.Context, cacheKey string, ref assets.SourceRef, dlReq *SourceDownloadRequest) (*assets.StagedAsset, error) {
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
