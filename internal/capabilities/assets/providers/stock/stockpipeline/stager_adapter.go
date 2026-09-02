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
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
	assets "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/ports"
)

type StockStager struct {
	svc         *Service
	downloader  SourceDownloader
	driveReader DriveReaderPort
	cacheReader SourceCacheReader
	cacheWriter SourceCacheWriter
	sf          singleflight.Group

	// sharedRefs maps each in-flight cacheKey to its reference-counted
	// lease on the leader's tmpDir file. sharedRefsMu serializes lease
	// lifecycle changes so an acquire cannot observe/delete a lease that
	// a concurrent release is replacing.
	sharedRefs   sync.Map // map[string]*sharedSourceLease (cacheKey → lease)
	sharedRefsMu sync.Mutex

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

func (s *StockStager) stageSource(ctx context.Context, ref assets.SourceRef) (result *assets.StagedAsset, err error) {
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
	var cacheLease *sharedSourceLease
	s.sharedRefsMu.Lock()
	if value, ok := s.sharedRefs.Load(cacheKey); ok {
		lease := value.(*sharedSourceLease)
		lease.mu.Lock()
		if !lease.released {
			lease.refCount++
			cacheLease = lease
			s.assetLeases.Store(outputPath, cacheKey)
		}
		lease.mu.Unlock()
	}
	s.sharedRefsMu.Unlock()
	if sa, hit := s.checkSourceCache(ctx, cacheKey, ref, outputPath, fs); hit {
		if isYouTubeSourceURL(ref.URL) {
			cacheMetric := startServiceStockPhase(ctx, "stock.youtube_download", "")
			if cacheMetric != nil {
				cacheMetric.SetItems(1, 1)
				cacheMetric.SetBytes(0, 0)
				finishServiceStockPhase(s.svc.log, cacheMetric, nil)
			}
		}
		return sa, nil
	}
	if cacheLease != nil {
		s.assetLeases.Delete(outputPath)
		_ = s.releaseSharedLease(cacheKey)
	}

	if isDriveURL(ref.URL) {
		sa, driveErr := s.stageFromDrive(ctx, ref, outputPath)
		if driveErr != nil {
			_ = fs.RemoveAll(tmpDir)
			s.assetLeases.Delete(outputPath)
			_ = s.releaseSharedLease(cacheKey)
			return nil, driveErr
		}
		s.populateCache(ctx, cacheKey, "drive", "", ref, outputPath, sa.Bytes)
		return sa, nil
	}

	if s.downloader == nil {
		s.assetLeases.Delete(outputPath)
		_ = fs.RemoveAll(tmpDir)
		_ = s.releaseSharedLease(cacheKey)
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

	finalLocalPath := outputPath
	lease, leader := s.reserveSharedLease(cacheKey, finalLocalPath)
	stagedAsset, sfErr := s.downloadSource(ctx, cacheKey, ref, dlReq)
	if sfErr != nil {
		s.assetLeases.Delete(finalLocalPath)
		_ = fs.RemoveAll(tmpDir)
		_ = s.releaseSharedLease(cacheKey)
		return nil, sfErr
	}

	leaderPath := stagedAsset.LocalPath
	if leader {
		// The downloader may return either a file created under our tmpDir
		// (direct yt-dlp) or a persistent file owned by another stager
		// (the production acquisition.SourceStager path). Only the former
		// belongs to this lease and may be removed when the last caller
		// releases it. Deleting an externally-owned path invalidates the
		// cross-run source cache and forces the next job to invoke yt-dlp
		// again.
		removeLeaderOnRelease := pathWithinDir(tmpDir, leaderPath)
		s.publishSharedLease(lease, leaderPath, removeLeaderOnRelease)
		if !removeLeaderOnRelease {
			// The first caller still owns its local tmpDir copy, but it must
			// not be treated as the owner of the external persistent source.
			// Clearing ownerPath makes cleanup remove this caller's tmpDir
			// while releaseSharedLease leaves leaderPath untouched.
			lease.mu.Lock()
			if lease.ownerPath == finalLocalPath {
				lease.ownerPath = ""
			}
			lease.mu.Unlock()
		}
	}
	if leaderPath != outputPath {
		if cpErr := copyFileToPath(leaderPath, outputPath, fs); cpErr != nil {
			s.assetLeases.Delete(finalLocalPath)
			_ = fs.RemoveAll(tmpDir)
			_ = s.releaseSharedLease(cacheKey)
			return nil, fmt.Errorf("stock stager: copy concurrent download: %w", cpErr)
		}
	}

	return &assets.StagedAsset{
		LocalPath: finalLocalPath,
		Bytes:     stagedAsset.Bytes,
	}, nil
}

func pathWithinDir(dir, path string) bool {
	dir = filepath.Clean(dir)
	path = filepath.Clean(path)
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func isYouTubeSourceURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "youtu.be" || strings.HasSuffix(host, ".youtube.com") || host == "youtube.com"
}

// ── acquisition.SourceStager adapter (Prepare / Release) ─────────────

// Compile-time assertion: StockStager satisfies acquisition.SourceStager.
var _ acquisition.SourceStager = (*StockStager)(nil)

// Prepare bridges the legacy StageSource method to the acquisition.SourceStager
// interface. The StockStager's StageSource already handles caching, shared leases,
// and Drive/YouTube staging — this adapter wraps the result into the
// acquisition.PrepareContext shape.
func (s *StockStager) Prepare(ctx context.Context, req acquisition.PrepareRequest) (*acquisition.PrepareContext, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	ref := assets.SourceRef{
		URL:             req.Source.URL,
		DownloadSection: req.Source.DownloadSection,
		ForceKeyframes:  req.Source.ForceKeyframes,
		MergeFormat:     req.Source.MergeFormat,
	}
	staged, err := s.stageSource(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", acquisition.ErrAcquisitionPrepareFailed, err)
	}
	if staged == nil {
		return nil, fmt.Errorf("%w: StageSource returned nil", acquisition.ErrAcquisitionPrepareFailed)
	}
	return &acquisition.PrepareContext{
		ID:           staged.SourceID,
		LocalPath:    staged.LocalPath,
		SizeBytes:    staged.Bytes,
		CleanupToken: staged.LocalPath,               // LocalPath doubles as cleanup token for legacy path
		ExpiresAt:    time.Now().Add(24 * time.Hour), // legacy path has no TTL
	}, nil
}

// Release bridges the legacy Cleanup method to the acquisition.SourceStager
// interface. The cleanupToken is the LocalPath from Prepare.
func (s *StockStager) Release(ctx context.Context, cleanupToken string) error {
	if cleanupToken == "" {
		return fmt.Errorf("%w: empty cleanup token", acquisition.ErrAcquisitionInvalidToken)
	}
	return s.cleanup(ctx, &assets.StagedAsset{
		LocalPath: cleanupToken,
	})
}
