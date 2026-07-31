// Package stockpipeline — stager_adapter.go.
//
// StockStager orchestrates source staging. Sub-files:
// stager_lease.go, stager_cache.go, stager_download.go,
// stager_drive.go, stager_cleanup.go.
package stockpipeline

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sync/singleflight"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	appmetrics "github.com/Marcuss-ops/PipelineGen/internal/application/processmetrics"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

var _ assets.SourceStager = (*StockStager)(nil)

type StockStager struct {
	svc         *Service
	downloader  SourceDownloader
	driveReader DriveReaderPort
	cacheReader SourceCacheReader
	cacheWriter SourceCacheWriter
	sf          singleflight.Group

	// sharedRefs maps each in-flight cacheKey to its reference-counted
	// lease on the leader's tmpDir file.
	sharedRefs sync.Map // map[string]*sharedSourceLease (cacheKey → lease)

	// assetLeases binds each caller's StagedAsset.LocalPath to the
	// cacheKey of the shared lease that caller acquired.
	// IMPORTANT: the key is the FINAL LocalPath returned to the
	// caller (post-copy), which differs between leader and follower.
	assetLeases sync.Map // map[string]string (LocalPath → cacheKey)
}

func NewStockStager(svc *Service) *StockStager {
	return &StockStager{svc: svc}
}

func (s *StockStager) WithDriveReader(r DriveReaderPort) *StockStager {
	s.driveReader = r
	return s
}

func (s *StockStager) WithSourceCache(reader SourceCacheReader, writer SourceCacheWriter) *StockStager {
	s.cacheReader = reader
	s.cacheWriter = writer
	return s
}

func (s *StockStager) WithDownloader(dl SourceDownloader) *StockStager {
	s.downloader = dl
	return s
}

func (s *StockStager) fs() (LocalFSPort, error) {
	if s.svc == nil || s.svc.localFS == nil {
		return nil, fmt.Errorf("stock stager: LocalFSPort not wired")
	}
	return s.svc.localFS, nil
}

func (s *StockStager) StageSource(ctx context.Context, ref assets.SourceRef) (result *assets.StagedAsset, err error) {
	if s.svc == nil {
		return nil, fmt.Errorf("stock stager: service not wired")
	}
	if ref.URL == "" {
		return nil, fmt.Errorf("stock stager: empty URL")
	}

	fs, fsErr := s.fs()
	if fsErr != nil {
		return nil, fsErr
	}

	tmpDir, err := fs.MkdirTemp(s.svc.runtime.WorkDir, "stock_stage_")
	if err != nil {
		return nil, fmt.Errorf("stock stager: create temp dir: %w", err)
	}

	outputPath := filepath.Join(tmpDir, "source.mp4")

	cacheKey := DeriveSourceCacheKey(ref.URL, ref.DownloadSection, ref.MergeFormat, ref.ForceKeyframes)
	if sa, hit := s.checkSourceCache(ctx, cacheKey, ref, outputPath, fs); hit {
		if isYouTubeSourceURL(ref.URL) && s.svc.metrics != nil {
			jobID, _ := appmetrics.RunIDs(ctx)
			cacheMetric := startServiceStockPhase(ctx, s.svc.metrics, "stock.youtube_download", jobID)
			cacheMetric.SetItems(1, 1)
			cacheMetric.SetBytes(0, 0)
			cacheMetric.SetDetails(map[string]any{
				"videos_downloaded":   0,
				"download_bytes":      0,
				"cache_hit":           true,
				"singleflight_shared": false,
			})
			finishServiceStockPhase(s.svc.log, cacheMetric, nil)
		}
		return sa, nil
	}

	if isDriveURL(ref.URL) {
		sa, driveErr := s.stageFromDrive(ctx, ref, outputPath)
		if driveErr != nil {
			_ = fs.RemoveAll(tmpDir)
			return nil, driveErr
		}
		s.populateCache(ctx, cacheKey, "drive", "", ref, outputPath, sa.Bytes)
		return sa, nil
	}

	if s.downloader == nil {
		return nil, fmt.Errorf("stock stager: downloader not wired")
	}

	dlReq := &SourceDownloadRequest{
		URL:        ref.URL,
		OutputPath: outputPath,
		NoPlaylist: true,
		// The downloader's shared BaseArgs resolver gates this to YouTube;
		// non-YouTube sources are unaffected.
		UseCookies: true,
	}
	if ref.DownloadSection != "" {
		dlReq.DownloadSections = []string{ref.DownloadSection}
		dlReq.ForceKeyframes = ref.ForceKeyframes
	}
	if ref.MergeFormat != "" {
		dlReq.MergeFormat = ref.MergeFormat
	}

	stagedAsset, sfErr := s.downloadSource(ctx, cacheKey, ref, dlReq)
	if sfErr != nil {
		_ = fs.RemoveAll(tmpDir)
		return nil, sfErr
	}

	leaderPath := stagedAsset.LocalPath
	finalLocalPath := leaderPath
	if leaderPath != outputPath {
		if cpErr := copyFileToPath(leaderPath, outputPath, fs); cpErr != nil {
			_ = fs.RemoveAll(tmpDir)
			return nil, fmt.Errorf("stock stager: copy concurrent download: %w", cpErr)
		}
		finalLocalPath = outputPath
	}

	s.acquireSharedLease(cacheKey, leaderPath, finalLocalPath)

	return &assets.StagedAsset{
		LocalPath: finalLocalPath,
		Bytes:     stagedAsset.Bytes,
	}, nil
}

func (s *StockStager) StageSourceV2(ctx context.Context, ref asset.SourceRef) (*asset.StagedSource, error) {
	staged, err := s.StageSource(ctx, assets.SourceRef(ref))
	if err != nil {
		return nil, err
	}
	return &asset.StagedSource{
		LocalPath: staged.LocalPath,
		Bytes:     staged.Bytes,
		SourceID:  ref.URL,
		SourceRef: ref,
	}, nil
}

func isYouTubeSourceURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "youtu.be" || strings.HasSuffix(host, ".youtube.com") || host == "youtube.com"
}

func (s *StockStager) CleanupStagedSource(ctx context.Context, staged *asset.StagedSource) error {
	if staged == nil {
		return nil
	}
	staged.CleanedUp = true
	return s.Cleanup(ctx, &assets.StagedAsset{
		LocalPath: staged.LocalPath,
		Bytes:     staged.Bytes,
	})
}
