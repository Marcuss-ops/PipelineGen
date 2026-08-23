package stockpipeline

import (
	"context"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/application/acquisition"
	appassets "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
	"go.uber.org/zap"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
)

func (s *StockStager) downloadSource(ctx context.Context, cacheKey string, ref appassets.SourceRef, req *SourceDownloadRequest) (*appassets.StagedAsset, error) {
	v, sfErr, _ := s.sf.Do(cacheKey, func() (interface{}, error) {
		var result *appassets.StagedAsset
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
		result = &appassets.StagedAsset{LocalPath: dlResult.ResolvedPath, Bytes: dlResult.SizeBytes}
		s.populateCache(ctx, cacheKey, "youtube", extractVideoIDFromURL(ref.URL), ref, dlResult.ResolvedPath, dlResult.SizeBytes)
		return result, nil
	})
	if sfErr != nil {
		return nil, sfErr
	}
	return v.(*appassets.StagedAsset), nil
}

// Cleanup removes the staged file's parent temp directory AND
// releases the shared-lease refcount (if any).
func (s *StockStager) cleanup(_ context.Context, staged *appassets.StagedAsset) error {
	if staged == nil || staged.LocalPath == "" {
		return nil
	}

	fs, fsErr := s.fs()
	if fsErr != nil {
		return fsErr
	}

	leaseKeyAny, hasLease := s.assetLeases.LoadAndDelete(staged.LocalPath)

	var ownErr error
	if hasLease {
		leaseKey, _ := leaseKeyAny.(string)
		if !s.isLeaseLeader(leaseKey, staged.LocalPath) {
			ownDir := filepath.Dir(staged.LocalPath)
			if ownDir != "" && ownDir != "." && ownDir != "/" {
				ownErr = fs.RemoveAll(ownDir)
			}
		}
		if rerr := s.releaseSharedLease(leaseKey); rerr != nil {
			s.assetLeases.Store(staged.LocalPath, leaseKey)
			if s.svc != nil && s.svc.log != nil {
				s.svc.log.Warn("stock stager: release shared lease failed",
					zap.String("lease_key", leaseKey),
					zap.Error(rerr))
			}
			if ownErr == nil {
				ownErr = rerr
			}
		}
		return ownErr
	}

	ownDir := filepath.Dir(staged.LocalPath)
	if ownDir == "" || ownDir == "." || ownDir == "/" {
		return nil
	}
	return fs.RemoveAll(ownDir)
}

func isDriveURL(rawURL string) bool {
	return strings.Contains(rawURL, "drive.google.com")
}

func extractDriveFileID(rawURL string) (string, error) {
	return urlutil.FileIDFromDriveLink(rawURL)
}

func (s *StockStager) stageFromDrive(ctx context.Context, ref appassets.SourceRef, outputPath string) (*appassets.StagedAsset, error) {
	if s.driveReader == nil {
		return nil, fmt.Errorf("stock stager: drive reader not wired")
	}

	fs, fsErr := s.fs()
	if fsErr != nil {
		return nil, fsErr
	}

	fileID, fileErr := extractDriveFileID(ref.URL)
	if fileErr != nil || fileID == "" {
		folderID := urlutil.FolderIDFromDriveLink(ref.URL)
		if folderID == "" {
			return nil, fmt.Errorf("stock stager: could not extract Drive file or folder ID from %q", ref.URL)
		}
		files, listErr := s.driveReader.ListFiles(ctx, folderID)
		if listErr != nil {
			return nil, fmt.Errorf("stock stager: list drive folder %q: %w", folderID, listErr)
		}
		for _, f := range files {
			if strings.HasPrefix(f.MimeType, "video/") {
				fileID = f.ID
				break
			}
		}
		if fileID == "" {
			return nil, fmt.Errorf("stock stager: no video file found in Drive folder %q", folderID)
		}
	}

	body, _, dlErr := s.driveReader.DownloadFile(ctx, fileID)
	if dlErr != nil {
		return nil, fmt.Errorf("stock stager: drive download file %q: %w", fileID, dlErr)
	}
	defer body.Close()

	f, createErr := fs.Create(outputPath)
	if createErr != nil {
		return nil, fmt.Errorf("stock stager: create output file: %w", createErr)
	}

	if _, copyErr := io.Copy(f, body); copyErr != nil {
		f.Close()
		return nil, fmt.Errorf("stock stager: write downloaded file: %w", copyErr)
	}

	if closeErr := f.Close(); closeErr != nil {
		return nil, fmt.Errorf("stock stager: close output file: %w", closeErr)
	}

	fi, statErr := fs.Stat(outputPath)
	if statErr != nil {
		return nil, fmt.Errorf("stock stager: stat downloaded file: %w", statErr)
	}

	return &appassets.StagedAsset{
		LocalPath: outputPath,
		Bytes:     fi.Size(),
	}, nil
}

func (s *StockStager) checkSourceCache(ctx context.Context, cacheKey string, ref appassets.SourceRef, outputPath string, fs LocalFSPort) (*appassets.StagedAsset, bool) {
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

	return &appassets.StagedAsset{
		LocalPath: outputPath,
		Bytes:     fi.Size(),
	}, true
}

func (s *StockStager) populateCache(ctx context.Context, cacheKey, provider, externalID string, ref appassets.SourceRef, localPath string, fileSize int64) {
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

type sharedSourceLease struct {
	mu              sync.Mutex
	path            string // downloader-resolved shared source path
	ownerPath       string // leader caller's final LocalPath
	refCount        int
	released        bool
	removeOnRelease bool
}

// reserveSharedLeaseLocked reserves a reference while sharedRefsMu is held.
// The caller is responsible for taking that lifecycle lock.
func (s *StockStager) reserveSharedLeaseLocked(cacheKey, ownerPath string) (*sharedSourceLease, bool) {
	if value, ok := s.sharedRefs.Load(cacheKey); ok {
		lease := value.(*sharedSourceLease)
		lease.mu.Lock()
		if !lease.released {
			lease.refCount++
			lease.mu.Unlock()
			s.assetLeases.Store(ownerPath, cacheKey)
			return lease, false
		}
		lease.mu.Unlock()
		s.sharedRefs.Delete(cacheKey)
	}

	lease := &sharedSourceLease{ownerPath: ownerPath, refCount: 1}
	s.sharedRefs.Store(cacheKey, lease)
	s.assetLeases.Store(ownerPath, cacheKey)
	return lease, true
}

func (s *StockStager) reserveSharedLease(cacheKey, ownerPath string) (*sharedSourceLease, bool) {
	s.sharedRefsMu.Lock()
	defer s.sharedRefsMu.Unlock()
	return s.reserveSharedLeaseLocked(cacheKey, ownerPath)
}

func (s *StockStager) publishSharedLease(lease *sharedSourceLease, sourcePath string, removeOnRelease bool) {
	lease.mu.Lock()
	if lease.path == "" {
		lease.path = sourcePath
		lease.removeOnRelease = removeOnRelease
	}
	lease.mu.Unlock()
}

func (s *StockStager) releaseSharedLease(cacheKey string) error {
	s.sharedRefsMu.Lock()
	defer s.sharedRefsMu.Unlock()

	value, ok := s.sharedRefs.Load(cacheKey)
	if !ok {
		return nil
	}
	lease := value.(*sharedSourceLease)
	lease.mu.Lock()
	if lease.released {
		lease.mu.Unlock()
		return nil
	}
	if lease.refCount > 0 {
		lease.refCount--
	}
	if lease.refCount != 0 {
		lease.mu.Unlock()
		return nil
	}

	lease.released = true
	leasePath := lease.path
	removeOnRelease := lease.removeOnRelease
	lease.mu.Unlock()

	// Keep the lifecycle lock until the final source removal completes.
	// No new reservation can otherwise slip between refcount zero and
	// removal of the shared source directory.
	var removeErr error
	if removeOnRelease && leasePath != "" {
		fs, fsErr := s.fs()
		if fsErr != nil {
			removeErr = fsErr
		} else {
			dir := filepath.Dir(leasePath)
			if dir != "" && dir != "." && dir != "/" {
				removeErr = fs.RemoveAll(dir)
			}
		}
	}
	if removeErr != nil {
		// Keep one retryable reference and the lease in sharedRefs. Cleanup
		// restores the asset binding so a later retry can remove the same
		// directory instead of leaking it permanently.
		lease.mu.Lock()
		lease.released = false
		lease.refCount = 1
		lease.mu.Unlock()
		return removeErr
	}
	s.sharedRefs.Delete(cacheKey)
	return nil
}

func (s *StockStager) isLeaseLeader(leaseKey, localPath string) bool {
	s.sharedRefsMu.Lock()
	defer s.sharedRefsMu.Unlock()
	value, ok := s.sharedRefs.Load(leaseKey)
	if !ok {
		return false
	}
	lease := value.(*sharedSourceLease)
	lease.mu.Lock()
	defer lease.mu.Unlock()
	return !lease.released && lease.ownerPath == localPath
}

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

// Package stockpipeline — source_staging.go (Stock P0 split, July 2026).
//
// This file owns the source-staging methods previously co-located in
// service.go: StageSource and stageSection. Both methods route through
// the canonical acquisition.SourceStager port (Stock Cutover §12-4).
//
// godlike/06 SSOT: one canonical owner for "how does stock fetch its
// source bytes?" — the acquisition.SourceStager port + these two methods.
// StageSource downloads a video from a URL and returns the staged file.
// It delegates to the canonical acquisition.SourceStager port which
// owns persistent stage registry + .meta.json sidecars + TTL eviction.
//
// §12-4 (July 2026): the legacy yt-dlp-baked local implementation is
// RETIRED. The Service no longer holds a `*downloader.YTDLPDownloader`
// field directly; instead it asks `Service.sourceStager.Prepare(ctx, req)`
// for the canonical PrepareContext + LocalPath. The TempPath + MkdirTemp
// dance is gone — stagingRoot lives in the FilesystemStager so multiple
// runs share persistent state across calls (idempotency invariant).
//
// Blocco 2a (July 2026, preserved for the FetchProvider contract): the
// returned *StagedSource is the legacy dual-shape carrier; the adapter
// flattens the PrepareContext.LocalPath + PrepareContext.SizeBytes
// into the StagedSource struct so callers (Adapter.Fetch etc.) don't
// need to switch shapes mid-call. The cleanup function is a thin
// wrapper around sourceStager.Release(ctx, PrepareContext.CleanupToken).
func (s *Service) StageSource(ctx context.Context, url string) (*StagedSource, error) {
	// P6 (July 2026): nil-guard for test-fixture path where
	// the acquisition.SourceStager is not wired at composition time.
	// ErrAcquisitionNotWired surfaces a typed sentinel callers can
	// errors.Is against.
	if s.sourceStager == nil {
		return nil, fmt.Errorf("stage source %q: %w", url, acquisition.ErrAcquisitionNotWired)
	}
	prepared, err := s.sourceStager.Prepare(ctx, acquisition.PrepareRequest{
		Source: acquisition.SourceRef{
			URL:           url,
			PolicyVersion: "v1",
		},
		IdempotencyKey: "stock.stage." + acquisition.DeriveIdempotencyKey(acquisition.SourceRef{
			URL:           url,
			PolicyVersion: "v1",
		}),
		CallerRef: "stock.StageSource",
	})
	if err != nil {
		return nil, fmt.Errorf("stage source: prepare via acquisition.SourceStager: %w", err)
	}
	fi, statErr := s.statLocal(prepared.LocalPath)
	if statErr != nil {
		return nil, fmt.Errorf("stage source: stat staged file %q: %w", prepared.LocalPath, statErr)
	}
	if fi.Size() == 0 {
		return nil, fmt.Errorf("stage source: staged file is empty: %s", prepared.LocalPath)
	}
	s.log.Info("stage source: video downloaded via acquisition port",
		zap.String("url", url),
		zap.String("local_path", prepared.LocalPath),
		zap.String("stage_id", prepared.ID),
		zap.String("cleanup_token", prepared.CleanupToken),
		zap.Int64("bytes", fi.Size()),
		zap.Time("expires_at", prepared.ExpiresAt),
	)
	return &StagedSource{
		LocalPath: prepared.LocalPath,
		Bytes:     fi.Size(),
	}, nil
}

// stageSection downloads a single time-slice of a video via the
// canonical acquisition.SourceStager port (Stock Cutover §12-4).
//
// §12-4 (July 2026): the section path no longer threads a raw
// downloader.YTDLPDownloader.Download call. Instead the section time
// range flows through the same acquisition.SourceRef envelope as the
// full-asset path; the yt-dlp invocation logic that handles yt-dlp's
// `--download-sections` lives INSIDE the production concrete
// (`*acquisition.YTDLPSourceStager`, §12-4.2 forward-pointer). Today
// the FilesystemStager concrete writes the file via its Fetch
// closure — which stock callers wire to the yt-dlp subprocess.
//
// The legacy `s.ytdlp.Download` direct call is RETIRED.
func (s *Service) stageSection(ctx context.Context, ref appassets.SourceRef) (*appassets.StagedAsset, error) {
	// P6 (July 2026): nil-guard for test-fixture path.
	if s.sourceStager == nil {
		return nil, fmt.Errorf("stage section %q: %w", ref.URL, acquisition.ErrAcquisitionNotWired)
	}
	prepared, err := s.sourceStager.Prepare(ctx, acquisition.PrepareRequest{
		Source: acquisition.SourceRef{
			URL:             ref.URL,
			DownloadSection: ref.DownloadSection,
			ForceKeyframes:  ref.ForceKeyframes,
			MergeFormat:     ref.MergeFormat,
			PolicyVersion:   "v1",
		},
		IdempotencyKey: "stock.section." + acquisition.DeriveIdempotencyKey(acquisition.SourceRef{
			URL:             ref.URL,
			DownloadSection: ref.DownloadSection,
			PolicyVersion:   "v1",
		}),
		CallerRef: "stock.stageSection",
	})
	if err != nil {
		return nil, fmt.Errorf("stage section: prepare via acquisition.SourceStager (%q section=%q): %w", ref.URL, ref.DownloadSection, err)
	}
	fi, statErr := s.statLocal(prepared.LocalPath)
	if statErr != nil {
		return nil, fmt.Errorf("stage section: stat %q: %w", prepared.LocalPath, statErr)
	}
	if fi.Size() == 0 {
		return nil, fmt.Errorf("stage section: staged file is empty: %s", prepared.LocalPath)
	}
	s.log.Info("stage section: video section downloaded via acquisition port",
		zap.String("url", ref.URL),
		zap.String("section", ref.DownloadSection),
		zap.String("local_path", prepared.LocalPath),
		zap.String("stage_id", prepared.ID),
		zap.String("cleanup_token", prepared.CleanupToken),
		zap.Int64("bytes", fi.Size()),
		zap.Time("expires_at", prepared.ExpiresAt),
	)
	return &appassets.StagedAsset{
		LocalPath: prepared.LocalPath,
		Bytes:     fi.Size(),
	}, nil
}

// statLocal delegates to s.localFS.Stat when the port is wired;
// returns an error when the port is nil (PR-REFACTOR-P0-IO-BINDER).
func (s *Service) statLocal(path string) (fs.FileInfo, error) {
	if s.localFS == nil {
		return nil, fmt.Errorf("stat %q: LocalFSPort not wired (composition root must inject filesystem.NewLocal())", path)
	}
	return s.localFS.Stat(path)
}
