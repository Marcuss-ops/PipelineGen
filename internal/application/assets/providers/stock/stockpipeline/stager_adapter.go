// Package stockpipeline — stager_adapter.go (Step 9/12, July 2026).
//
// StockStager wraps stockpipeline.Service.StageSource behind the
// canonical assets.SourceStager port so callers can stage stock
// source media without depending on the full stockpipeline.Service.
//
// July 2026 (DIRECT-YTDLP): StockStager downloads directly via
// yt-dlp instead of routing through Service.StageSource →
// acquisition.SourceStager.Prepare. The acquisition chain causes
// nil-deref when sourceStager is not wired at composition root;
// the yt-dlp direct path is the production-tested download path.
//
// Google Drive download (July 2026): when a URL points to Drive
// (drive.google.com), the stager routes through DriveDownloaderPort
// instead of yt-dlp. The port mirrors drive.Reader.DownloadFile so
// the concrete *drive.Uploader satisfies it without an adapter.
// Composition root wires the port via WithDriveDownloader.
package stockpipeline

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// Compile-time assertion: *StockStager satisfies assets.SourceStager.
var _ assets.SourceStager = (*StockStager)(nil)

// DriveDownloaderPort is the Pattern 0 port for downloading files
// from Google Drive. The port mirrors drive.Reader.DownloadFile so
// the concrete *drive.Uploader satisfies it without an adapter.
//
// godlike/06 SSOT: this port lives in the stockpipeline package
// (application layer); the concrete implementation lives in
// internal/infrastructure/drive/ and is injected via
// WithDriveDownloader at composition time.
type DriveDownloaderPort interface {
	DownloadFile(ctx context.Context, fileID string) (io.ReadCloser, string, error)
}

// StockStager adapts a stockpipeline.Service to the shared
// assets.SourceStager port. It downloads directly via yt-dlp
// (YouTube/DirectURLs) and via DriveDownloaderPort (Google Drive
// URLs), bypassing the acquisition.SourceStager chain.
//
// Source cache (July 2026): when a SourceCacheReader + SourceCacheWriter
// are wired via WithSourceCache, the stager checks the SQLite-backed
// cache before invoking yt-dlp. Cache hits copy the cached file into
// the new temp directory (no re-download). Cache misses trigger the
// normal download path and populate the cache on success.
//
// Download concurrency (godlike/06 SSOT, July 2026): the yt-dlp
// download path is wrapped in a singleflight.Group keyed by cacheKey.
// Two concurrent StageSource calls on the same URL collapse to ONE
// yt-dlp download — the second goroutine blocks until the first
// finishes, then receives the same *assets.StagedAsset. DoD §8
// ("2 richieste simultanee collassino a 1 download") is enforced
// here. The singleflight callback's return value is cast back to
// *assets.StagedAsset at the call site.
//
// godlike/06 SSOT (concrete ownership): the singleflight.Group is
// owned by the StockStager struct (one per stager instance, the same
// scope as the cache reader/writer). The singleflight key is the
// canonical DeriveSourceCacheKey hash (download-section sensitive:
// two ranges on the same source URL hit different keys → two
// potential downloads, as expected by DoD §7 "Clip A vs Clip B").
type StockStager struct {
	svc             *Service
	downloader      DownloaderPort
	driveDownloader DriveDownloaderPort
	cacheReader     SourceCacheReader
	cacheWriter     SourceCacheWriter
	sf              singleflight.Group
}

// NewStockStager wraps a stockpipeline.Service as an assets.SourceStager.
// svc must be non-nil; nil produces a runtime error on StageSource.
// The downloader is constructed from the service's config (the
// concrete *downloader.YTDLPDownloader satisfies DownloaderPort
// structurally).
//
// Google Drive download support is wired separately via
// WithDriveDownloader (optional — nil means Drive URLs fall through
// to the downloader, which will fail with a descriptive error).
//
// Downloader override is wired separately via WithDownloader
// (optional — null means use the default constructed from svc.cfg).
func NewStockStager(svc *Service) *StockStager {
	var dl DownloaderPort
	if svc != nil && svc.cfg != nil {
		dl = downloader.NewYTDLP(svc.cfg)
	}
	return &StockStager{svc: svc, downloader: dl}
}

// WithDriveDownloader threads a Google Drive downloader into the
// stager. When non-nil, StageSource routes drive.google.com URLs
// through the Drive API instead of yt-dlp. Returns the receiver for
// fluent chaining.
//
// The canonical concrete implementation is *drive.Uploader (which
// satisfies DriveDownloaderPort structurally via its DownloadFile
// method). Composition root injects it via the DriveBundle.
func (s *StockStager) WithDriveDownloader(dl DriveDownloaderPort) *StockStager {
	s.driveDownloader = dl
	return s
}

// WithSourceCache threads a cross-run source download cache into the
// stager. When both reader and writer are non-nil, StageSource checks
// the SQLite-backed cache before invoking yt-dlp and populates it
// after a successful download. Returns the receiver for fluent chaining.
//
// Cache key is derived from the canonical URL + download parameters
// via DeriveSourceCacheKey. The cache is invalidated when the cached
// file is missing on disk or has a size mismatch.
func (s *StockStager) WithSourceCache(reader SourceCacheReader, writer SourceCacheWriter) *StockStager {
	s.cacheReader = reader
	s.cacheWriter = writer
	return s
}

// WithDownloader overrides the default downloader. The composition
// root or test fixture may inject a custom DownloaderPort (e.g. a
// test fake that counts calls and gates operations). Returns the
// receiver for fluent chaining. nil is allowed but surfaces a typed
// error on StageSource's download path (godlike/07 fail-closed).
func (s *StockStager) WithDownloader(dl DownloaderPort) *StockStager {
	s.downloader = dl
	return s
}

// StageSource implements assets.SourceStager. Downloads the source video
// directly via yt-dlp (YouTube/DirectURLs) or via DriveDownloaderPort
// (Google Drive file URLs), bypassing the acquisition.SourceStager chain.
//
// URL detection: if the URL contains "drive.google.com", the stager
// extracts the file ID and downloads via the Drive API. All other URLs
// flow through yt-dlp.
func (s *StockStager) StageSource(ctx context.Context, ref assets.SourceRef) (*assets.StagedAsset, error) {
	if s.svc == nil {
		return nil, fmt.Errorf("stock stager: service not wired")
	}
	if ref.URL == "" {
		return nil, fmt.Errorf("stock stager: empty URL")
	}

	// Create a temp staging directory under the service's temp path.
	tmpDir, err := os.MkdirTemp(s.svc.cfg.Storage.TempPath(), "stock_stage_")
	if err != nil {
		return nil, fmt.Errorf("stock stager: create temp dir: %w", err)
	}

	outputPath := filepath.Join(tmpDir, "source.mp4")

	// ── Source cache lookup (cross-run dedup) ────────────────
	// Before downloading, check the SQLite-backed cache for a
	// previously downloaded copy of the same source. Cache key
	// is derived from the canonical URL + download parameters.
	cacheKey := DeriveSourceCacheKey(ref.URL, ref.DownloadSection, ref.MergeFormat, ref.ForceKeyframes)
	if s.cacheReader != nil {
		if cached, cacheErr := s.cacheReader.GetByCacheKey(ctx, cacheKey); cacheErr == nil && cached != nil {
			if validateErr := validateCacheHit(cached, s.svc.localFS, s.svc.log); validateErr == nil {
				if s.svc.log != nil {
					s.svc.log.Info("stock stager: SOURCE_CACHE_HIT",
						zap.String("cache_key", cacheKey[:16]+"..."),
						zap.String("source_url", ref.URL),
						zap.String("cached_path", cached.LocalPath))
				}
				// Copy cached file into the new temp directory.
				if cpErr := copyFileToPath(cached.LocalPath, outputPath, s.svc.localFS); cpErr != nil {
					if s.svc.log != nil {
						s.svc.log.Warn("stock stager: cache hit but copy failed, falling through to download",
							zap.String("cache_key", cacheKey[:16]+"..."),
							zap.Error(cpErr))
					}
				} else {
					fi, statErr := os.Stat(outputPath)
					if statErr == nil {
						return &assets.StagedAsset{
							LocalPath: outputPath,
							Bytes:     fi.Size(),
						}, nil
					}
				}
			} else {
				// Cache hit but file invalid — invalidate entry.
				if s.cacheWriter != nil {
					_ = s.cacheWriter.Invalidate(ctx, cacheKey)
				}
			}
		}
	}

	// Google Drive URL → download via Drive API.
	if isDriveURL(ref.URL) {
		sa, driveErr := s.stageFromDrive(ctx, ref, outputPath)
		if driveErr != nil {
			os.RemoveAll(tmpDir)
			return nil, driveErr
		}
		// Populate cache for Drive downloads.
		s.populateCache(ctx, cacheKey, "drive", "", ref, outputPath, sa.Bytes)
		return sa, nil
	}

	if s.downloader == nil {
		return nil, fmt.Errorf("stock stager: downloader not wired (cfg nil or WithDownloader not called)")
	}

	dlReq := &downloader.DownloadRequest{
		URL:        ref.URL,
		OutputPath: outputPath,
		NoPlaylist: true,
		UseCookies: true,
	}
	if ref.DownloadSection != "" {
		dlReq.DownloadSections = []string{ref.DownloadSection}
		dlReq.ForceKeyframes = ref.ForceKeyframes
	}
	if ref.MergeFormat != "" {
		dlReq.MergeFormat = ref.MergeFormat
	}

	// ── Concurrent download collapse (godlike/06 SSOT) ─────────────
	// Two concurrent StageSource calls on the same cacheKey collapse
	// to ONE yt-dlp download. The leader downloads + populates cache;
	// followers block until the leader finishes, then receive the
	// same *assets.StagedAsset pointer.
	//
	// The singleflight key is the same DeriveSourceCacheKey hash used
	// for the cache lookup, so cache hits AND same-key downloads
	// collapse uniformly. Different download-sections yield different
	// keys (no false collapse between Clip A and Clip B on the same
	// source — see DoD §7 in the runbook + source_cache_test.go::T4).
	v, sfErr, _ := s.sf.Do(cacheKey, func() (interface{}, error) {
		if dlErr := s.downloader.Download(ctx, dlReq); dlErr != nil {
			return nil, fmt.Errorf("stock stager: yt-dlp download %q: %w", ref.URL, dlErr)
		}
		// Resolve the actual downloaded file path.
		resolved, resolveErr := downloader.ResolveDownloadedSegmentPath(outputPath + ".%(ext)s")
		if resolveErr != nil {
			return nil, fmt.Errorf("stock stager: resolve downloaded file: %w", resolveErr)
		}
		fi, statErr := os.Stat(resolved)
		if statErr != nil {
			return nil, fmt.Errorf("stock stager: stat %q: %w", resolved, statErr)
		}
		// Populate cache for fresh downloads (best-effort, never surfaces).
		s.populateCache(ctx, cacheKey, "youtube", extractVideoIDFromURL(ref.URL), ref, resolved, fi.Size())
		return &assets.StagedAsset{
			LocalPath: resolved,
			Bytes:     fi.Size(),
		}, nil
	})
	if sfErr != nil {
		// Cleanup this caller's tmp dir (leader's tmp was different
		// — followers don't write any file so their tmpDir is empty
		// and is left in place for Cleanup() downstream).
		os.RemoveAll(tmpDir)
		return nil, sfErr
	}
	stagedAsset := v.(*assets.StagedAsset)
	// If the staged asset returned points to another caller's temp path,
	// copy it to our own unique temp folder to avoid concurrency races
	// (when Job A deletes its directory on cleanup while Job B is still reading).
	if stagedAsset.LocalPath != outputPath {
		if cpErr := copyFileToPath(stagedAsset.LocalPath, outputPath, s.svc.localFS); cpErr != nil {
			os.RemoveAll(tmpDir)
			return nil, fmt.Errorf("stock stager: copy concurrent download from %s to %s: %w", stagedAsset.LocalPath, outputPath, cpErr)
		}
		return &assets.StagedAsset{
			LocalPath: outputPath,
			Bytes:     stagedAsset.Bytes,
		}, nil
	}
	return stagedAsset, nil
}

// populateCache writes a successful download to the source cache.
// Failures are logged but never surface to the caller (best-effort).
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
			s.svc.log.Warn("stock stager: failed to populate source cache (best-effort)",
				zap.String("cache_key", cacheKey[:16]+"..."),
				zap.Error(err))
		}
	}
}

// Cleanup removes the staged file's parent temp directory.
func (s *StockStager) Cleanup(_ context.Context, staged *assets.StagedAsset) error {
	if staged == nil || staged.LocalPath == "" {
		return nil
	}
	dir := filepath.Dir(staged.LocalPath)
	if dir == "" || dir == "." || dir == "/" {
		return nil
	}
	return os.RemoveAll(dir)
}

// ── Drive download helpers ─────────────────────────────────────────────

// isDriveURL reports whether rawURL points to a Google Drive file
// (as opposed to a YouTube URL or any other source). The check is
// a simple host match so callers can route Drive URLs through the
// Drive API without inspecting the full URL scheme.
func isDriveURL(rawURL string) bool {
	return strings.Contains(rawURL, "drive.google.com")
}

// extractDriveFileID extracts the canonical Google Drive file ID
// from a Drive file URL. Delegates to pkg/urlutil.FileIDFromDriveLink
// which supports the 5 canonical URL shapes (file/d/<id>/view,
// file/d/<id>/edit, uc?id=<id>, open?id=<id>, bare <id>).
func extractDriveFileID(rawURL string) (string, error) {
	return urlutil.FileIDFromDriveLink(rawURL)
}

// stageFromDrive downloads a file from Google Drive via the
// DriveDownloaderPort and writes it to outputPath. Returns a
// *StagedAsset pointing at the downloaded file on success.
//
// godlike/07 typed-error contract: fileID extraction failure,
// unwired drive downloader, Drive API errors, and local I/O
// errors each surface as typed wraps (%w) so callers can
// errors.Is/As probe the underlying cause.
func (s *StockStager) stageFromDrive(ctx context.Context, ref assets.SourceRef, outputPath string) (*assets.StagedAsset, error) {
	if s.driveDownloader == nil {
		return nil, fmt.Errorf("stock stager: drive downloader not wired (use WithDriveDownloader at composition time)")
	}

	fileID, err := extractDriveFileID(ref.URL)
	if err != nil {
		return nil, fmt.Errorf("stock stager: extract drive file ID from %q: %w", ref.URL, err)
	}
	if fileID == "" {
		return nil, fmt.Errorf("stock stager: empty file ID extracted from %q", ref.URL)
	}

	body, _, dlErr := s.driveDownloader.DownloadFile(ctx, fileID)
	if dlErr != nil {
		return nil, fmt.Errorf("stock stager: drive download file %q: %w", fileID, dlErr)
	}
	defer body.Close()

	f, createErr := os.Create(outputPath)
	if createErr != nil {
		return nil, fmt.Errorf("stock stager: create output file %q: %w", outputPath, createErr)
	}

	if _, copyErr := io.Copy(f, body); copyErr != nil {
		f.Close()
		return nil, fmt.Errorf("stock stager: write downloaded file to %q: %w", outputPath, copyErr)
	}

	// Close explicitly so the stat below sees the full file size.
	if closeErr := f.Close(); closeErr != nil {
		return nil, fmt.Errorf("stock stager: close output file: %w", closeErr)
	}

	fi, statErr := os.Stat(outputPath)
	if statErr != nil {
		return nil, fmt.Errorf("stock stager: stat downloaded file %q: %w", outputPath, statErr)
	}

	return &assets.StagedAsset{
		LocalPath: outputPath,
		Bytes:     fi.Size(),
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
