package stockpipeline

import (
	"context"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
)

func (s *StockStager) checkSourceCache(ctx context.Context, cacheKey string, ref assets.SourceRef, outputPath string, fs LocalFSPort) (*assets.StagedAsset, bool) {
	if s.cacheReader == nil {
		return nil, false
	}

	cached, cacheErr := s.cacheReader.GetByCacheKey(ctx, cacheKey)
	if cacheErr != nil || cached == nil {
		return nil, false
	}

	if validateErr := validateCacheHit(cached, fs, s.svc.log); validateErr != nil {
		if s.cacheWriter != nil {
			_ = s.cacheWriter.Invalidate(ctx, cacheKey)
		}
		return nil, false
	}

	if s.svc.log != nil {
		s.svc.log.Info("stock stager: SOURCE_CACHE_HIT",
			zap.String("cache_key", cacheKey[:16]+"..."),
			zap.String("source_url", ref.URL),
			zap.String("cached_path", cached.LocalPath))
	}

	if cpErr := copyFileToPath(cached.LocalPath, outputPath, fs); cpErr != nil {
		if s.svc.log != nil {
			s.svc.log.Warn("stock stager: cache hit but copy failed",
				zap.String("cache_key", cacheKey[:16]+"..."),
				zap.Error(cpErr))
		}
		return nil, false
	}

	fi, statErr := fs.Stat(outputPath)
	if statErr != nil {
		return nil, false
	}

	return &assets.StagedAsset{
		LocalPath: outputPath,
		Bytes:     fi.Size(),
	}, true
}

func (s *StockStager) populateCache(ctx context.Context, cacheKey, provider, externalID string, ref assets.SourceRef, localPath string, fileSize int64) {
	if s.cacheWriter == nil {
		return
	}
	entry := &SourceCacheEntry{
		CacheKey:        cacheKey,
		Provider:        provider,
		ExternalID:      externalID,
		SourceURL:       ref.URL,
		LocalPath:       localPath,
		FileSize:        fileSize,
		DownloadSection: ref.DownloadSection,
		MergeFormat:     ref.MergeFormat,
		ForceKeyframes:  ref.ForceKeyframes,
	}
	if err := s.cacheWriter.Upsert(ctx, entry); err != nil {
		if s.svc != nil && s.svc.log != nil {
			s.svc.log.Warn("stock stager: failed to populate source cache",
				zap.String("cache_key", cacheKey[:16]+"..."),
				zap.Error(err))
		}
	}
}
