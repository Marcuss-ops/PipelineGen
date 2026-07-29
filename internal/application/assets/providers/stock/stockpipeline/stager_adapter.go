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
// (drive.google.com), the stager routes through DriveReaderPort
// instead of yt-dlp. The port mirrors drive.Reader so the concrete
// *drive.Uploader satisfies it without an adapter. Folder URLs are
// expanded by listing the folder and picking the first video file.
// Composition root wires the port via WithDriveReader.
package stockpipeline

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"

	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// Compile-time assertion: *StockStager satisfies assets.SourceStager.
var _ assets.SourceStager = (*StockStager)(nil)

// sharedSourceLease carries the reference count + leader path for a
// single in-flight singleflight download. Mutex-guarded for
// concurrent acquire/release from many goroutines.
//
// PR-STOCK-SOURCE-CACHE-LEASE (July 2026, P0 race fix): each
// StageSource caller that goes through the singleflight path
// acquires a ref on the leader's tmpDir; the LAST caller to release
// physically unlinks it AND evicts this lease from the sharedRefs
// map so a future acquireSharedLease on the same cacheKey can
// create a fresh lease without inheriting the sticky released=true
// state. acquisition is via acquireSharedLease (called from
// StageSource) and release is via releaseSharedLease (called from
// Cleanup). Together with the copy-to-own-tmp layer they eliminate
// the "Job A Cleanup deletes the source while Job B is still
// reading it" race window identified by the verdict.
type sharedSourceLease struct {
	mu       sync.Mutex
	path     string
	refCount int
	released bool
}

// StockStager adapts a stockpipeline.Service to the shared
// assets.SourceStager port. It downloads directly via yt-dlp
// (YouTube/DirectURLs) and via DriveReaderPort (Google Drive
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
//
// Source-cache concurrent safety (PR-STOCK-SOURCE-CACHE-LEASE,
// July 2026): under singleflight, N concurrent StageSource callers
// collapse to ONE yt-dlp download and each caller initially observes
// the same leader pointer. Two complementary safety layers guarantee
// the leader's tmpDir cannot be unlinked out from under other
// concurrent readers:
//
//	(a) Copy-to-own-tmp — after the singleflight callback returns,
//	    each follower copies the leader's file into its own unique
//	    tmpDir/source.mp4. Cleanup of a follower removes only its
//	    own tmpDir, so the leader's file is unaffected. The copy
//	    step is also robust because it uses an open file
//	    descriptor; even if the leader's parent directory is
//	    unlinked mid-copy, the FD keeps the file alive until close.
//
//	(b) Reference-counted lease — caller-specific Lease awareness
//	    binds the StagedAsset.LocalPath returned to each caller to
//	    an entry in sharedRefs. Cleanup uses isLeaseLeader to detect
//	    whether the calling staged asset IS the leader (LocalPath
//	    == lease.path) or a follower (LocalPath != lease.path). For
//	    the leader, Cleanup defers tmpDir removal entirely to the
//	    lease (which unlinks only when refCount==0). For followers,
//	    Cleanup removes the caller's own tmpDir directly AND releases
//	    one ref on the lease. Sticky `released=true` is avoided by
//	    deleting the lease from sharedRefs once its refCount
//	    reaches zero, so a future acquireSharedLease on the same
//	    cacheKey (cross-round reuse within the StockStager lifetime)
//	    creates a fresh lease and properly unlinks its leader path.
//
// Layer (a) is the primary guarantee (each caller fully isolated).
// Layer (b) protects the small under-the-hood window where the
// leader's own Cleanup would otherwise eagerly unlink the leader's
// tmpDir, defeating the FD-based protection of (a).
type StockStager struct {
	svc         *Service
	downloader  DownloaderPort
	driveReader DriveReaderPort
	cacheReader SourceCacheReader
	cacheWriter SourceCacheWriter
	sf          singleflight.Group

	// sharedRefs maps each in-flight cacheKey to its reference-counted
	// lease on the leader's tmpDir file. acquireSharedLease /
	// releaseSharedLease own the lifecycle. The map is per-StockStager
	// instance and is in-memory only; it's cleared when the process
	// exits. The cross-run SQLite cache is the persistent owner of
	// the source file.
	sharedRefs sync.Map // map[string]*sharedSourceLease (cacheKey → lease)

	// assetLeases binds each caller's StagedAsset.LocalPath to the
	// cacheKey of the shared lease that caller acquired. Cleanup
	// uses this side-map to find and release the lease without
	// requiring lease plumbing on the assets.SourceStager port
	// surface. Loaded once per StageSource call (store), then
	// LoadAndDelete'd on Cleanup.
	//
	// IMPORTANT: the key is the FINAL LocalPath returned to the
	// caller (post-copy), which differs between leader and follower:
	//   - leader: returns stagedAsset.LocalPath (= resolved path on
	//     disk, may equal or differ from its raw outputPath)
	//   - follower: returns outputPath (its own copy)
	// Mixed-up keys (e.g. always storing outputPath) caused earlier
	// iterations of this fix to leave the leader's Cleanup un-leased.
	assetLeases sync.Map // map[string]string (LocalPath → cacheKey)
}

// NewStockStager wraps a stockpipeline.Service as an assets.SourceStager.
// svc must be non-nil; nil produces a runtime error on StageSource.
// The downloader is supplied by the composition root through WithDownloader.
//
// Google Drive download support is wired separately via
// WithDriveReader (optional — nil means Drive URLs fall through
// to the downloader, which will fail with a descriptive error).
//
// Downloader override is wired separately via WithDownloader
// (optional — nil means the composition root did not wire a downloader).
func NewStockStager(svc *Service) *StockStager {
	return &StockStager{svc: svc}
}

// WithDriveReader threads a Google Drive reader into the stager.
// When non-nil, StageSource routes drive.google.com URLs through
// the Drive API instead of yt-dlp, and supports both file URLs and
// folder URLs (the first video file in the folder is chosen).
// Returns the receiver for fluent chaining.
//
// The canonical concrete implementation is *drive.Uploader (which
// satisfies DriveReaderPort structurally). Composition root injects
// it via the DriveBundle.
func (s *StockStager) WithDriveReader(r DriveReaderPort) *StockStager {
	s.driveReader = r
	return s
}

// WithSourceCache threads a cross-run source download cache into the
// stager. When both reader and writer are non-nil, StageSource checks
// the SQLite-backed cache before invoking yt-dlp and populates it
// after a successful download. Returns the receiver for fluent
// chaining.
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

// acquireSharedLease initializes the per-cacheKey lease on first
// call (path/leaderPath are stamped; refCount starts at 0 and is
// bumped to 1 inside this method), or increments refCount of an
// already-active lease. The side-map assetLeases binds the caller's
// StagedAsset.LocalPath (the path actually returned) to the
// cacheKey so Cleanup can find the lease without forcing call sites
// to thread lease keys through the assets.SourceStager port.
//
// godlike/06 SSOT: this lease is owned exclusively by StockStager;
// no caller code path may mutate refCount directly. refCount is
// initialized to 0 (NOT 1) before ++ so that:
//
//   - a single acquire + single release → refCount 1→0 → unlink
//   - evict (B2 fix prevents sticky released=true on reuse);
//   - N acquires and N releases → refCount N→0 → unlink + evict
//     (no count leak of +1 per acquire).
func (s *StockStager) acquireSharedLease(cacheKey, leaderPath, callLocalPath string) {
	// TOCTOU robustness (post-review R2): a concurrent new acquire
	// may race with releaseSharedLease's Delete. If the new acquire
	// inherits a `released=true` lease, the matching release()
	// short-circuits and the new leader's tmpDir is never reclaimed.
	// Retry on `lease.released` to evict the stale entry and let
	// LoadOrStore create a fresh lease. Bounded to a small number
	// of attempts (pathological wheel-spin is bounded by the limit).
	const maxAttempts = 4
	for attempt := 0; attempt < maxAttempts; attempt++ {
		// R3 cosmetic: don't pre-init `path` here; we set it under the
		// lock so readers (isLeaseLeader, releaseSharedLease) always
		// see a fully-stamped lease.
		leaseI, _ := s.sharedRefs.LoadOrStore(cacheKey, &sharedSourceLease{})
		lease := leaseI.(*sharedSourceLease)
		lease.mu.Lock()
		if lease.released {
			lease.mu.Unlock()
			s.sharedRefs.Delete(cacheKey)
			continue
		}
		lease.path = leaderPath
		lease.refCount++
		lease.mu.Unlock()
		s.assetLeases.Store(callLocalPath, cacheKey)
		return
	}
	// Pathological wheel-spin fallback after maxAttempts: best-effort
	// acquire with a forcibly fresh lease. Worst case (still contended)
	// is a slight refCount drift, which the next release iterations
	// absorb via the sticky `released=true` guard.
	s.sharedRefs.Delete(cacheKey)
	leaseI, _ := s.sharedRefs.LoadOrStore(cacheKey, &sharedSourceLease{})
	lease := leaseI.(*sharedSourceLease)
	lease.mu.Lock()
	lease.path = leaderPath
	lease.refCount++
	lease.mu.Unlock()
	s.assetLeases.Store(callLocalPath, cacheKey)
}

// releaseSharedLease decrements the in-flight refcount for cacheKey.
// When refCount hits 0, the lease unlinks the leader's tmpDir AND
// evicts the lease from sharedRefs (so a future acquireSharedLease
// on the same cacheKey creates a fresh lease — prevents the
// sticky `released=true` leak described in B2). Returns the
// underlying os.RemoveAll error so callers can log it; never
// bubbles up because Cleanup is best-effort.
//
// Idempotent: a second release on an evicted key returns nil
// (sync.Map.Load returns ok=false). A second release on a lease
// still in sharedRefs but already at refCount==0 also returns
// nil (`released` sticky guard under lock).
func (s *StockStager) releaseSharedLease(cacheKey string) error {
	val, ok := s.sharedRefs.Load(cacheKey)
	if !ok {
		return nil
	}
	lease := val.(*sharedSourceLease)
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.released {
		return nil
	}
	if lease.refCount > 0 {
		lease.refCount--
	}
	if lease.refCount == 0 {
		lease.released = true
		var unlinkErr error
		if lease.path != "" {
			dir := filepath.Dir(lease.path)
			if dir != "" && dir != "." && dir != "/" {
				unlinkErr = s.svc.localFS.RemoveAll(dir)
			}
		}
		// Evict BEFORE unlocking so a concurrent new acquireSharedLease
		// that goes through sync.Map.LoadOrStore after our Delete sees
		// the key absent and creates a fresh lease (path reset,
		// refCount init=0, released=false). The previous acquireSharedLease
		// would have raced into stale-released=true territory without
		// this Delete; with it, the lease is gracefully recycled on
		// reuse of the same cacheKey.
		s.sharedRefs.Delete(cacheKey)
		return unlinkErr
	}
	return nil
}

// isLeaseLeader reports whether the staged asset identified by
// localPath was the LEADER caller of the given cacheKey's lease
// (LocalPath == lease.path AND the lease is still active). Cleanup
// uses this to decide whether the caller's own tmpDir (== leader's
// tmpDir) must be deferred to the lease's last-ref unlink, or
// removed directly (follower case where own tmpDir is independent).
//
// godlike/07 honest-limitation: this is best-effort — a race
// between isLeaseLeader and releaseSharedLease can drop one side by
// a few microseconds, but since both are mutex-guarded on the same
// lease.mu and Cleanup is best-effort, the worst case is a stale
// leader detection that simply performs the wrong branch — the
// subsequent os.RemoveAll on a missing dir is idempotent and the
// lease's eviction handles the leader tmpDir correctly anyway.
func (s *StockStager) isLeaseLeader(leaseKey, localPath string) bool {
	val, ok := s.sharedRefs.Load(leaseKey)
	if !ok {
		return false
	}
	lease := val.(*sharedSourceLease)
	lease.mu.Lock()
	defer lease.mu.Unlock()
	return !lease.released && lease.path == localPath
}

// StageSource implements assets.SourceStager. Downloads the source video
// directly via yt-dlp (YouTube/DirectURLs) or via DriveReaderPort
// (Google Drive file or folder URLs), bypassing the
// acquisition.SourceStager chain.
//
// URL detection: if the URL contains "drive.google.com", the stager
// extracts the file ID and downloads via the Drive API. If the URL is
// a folder, the stager lists the folder contents and picks the first
// video file. All other URLs flow through yt-dlp.
func (s *StockStager) StageSource(ctx context.Context, ref assets.SourceRef) (*assets.StagedAsset, error) {
	if s.svc == nil {
		return nil, fmt.Errorf("stock stager: service not wired")
	}
	if ref.URL == "" {
		return nil, fmt.Errorf("stock stager: empty URL")
	}

	// Create a temp staging directory under the service's temp path.
	tmpDir, err := s.svc.localFS.MkdirTemp(s.svc.runtime.WorkDir, "stock_stage_")
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
				fi, statErr := s.svc.localFS.Stat(outputPath)
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
		_ = s.svc.localFS.RemoveAll(tmpDir)
		return nil, driveErr
		}
		// Populate cache for Drive downloads.
		s.populateCache(ctx, cacheKey, "drive", "", ref, outputPath, sa.Bytes)
		return sa, nil
	}

	if s.downloader == nil {
		return nil, fmt.Errorf("stock stager: downloader not wired (WithDownloader was not called)")
	}

	dlReq := &downloader.DownloadRequest{
		URL:        ref.URL,
		OutputPath: outputPath,
		NoPlaylist: true,
		// Public stock videos must use the Android-capable path. Passing
		// browser cookies forces yt-dlp onto the web client and can trigger
		// YouTube's n-challenge/bot gate; cookies remain available for
		// authenticated metadata/subtitle flows that explicitly opt in.
		UseCookies: false,
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
	// PR-STOCK-SOURCE-CACHE-LEASE (July 2026): every caller that
	// goes through this branch acquires a ref on a per-cacheKey
	// lease (sharedSourceLease). The lease guards the leader's
	// tmpDir: only the LAST outstanding ref physically unlinks
	// (via releaseSharedLease in Cleanup) AND evicts the lease from
	// sharedRefs. With the finalLocalPath fix (B3), acquireSharedLease
	// runs AFTER the copy decision and binds the assetLeases side-map
	// key to the path actually returned to the caller (leader or
	// follower's own copy), so Cleanup's leader-detection correctly
	// defers leader-tmpDir removal to the lease's last-ref unlink.
	v, sfErr, _ := s.sf.Do(cacheKey, func() (interface{}, error) {
		if dlErr := s.downloader.Download(ctx, dlReq); dlErr != nil {
			return nil, fmt.Errorf("stock stager: yt-dlp download %q: %w", ref.URL, dlErr)
		}
		// Resolve the actual downloaded file path.
		resolved, resolveErr := downloader.ResolveDownloadedSegmentPath(outputPath + ".%(ext)s")
		if resolveErr != nil {
			return nil, fmt.Errorf("stock stager: resolve downloaded file: %w", resolveErr)
		}
		fi, statErr := s.svc.localFS.Stat(resolved)
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
		_ = s.svc.localFS.RemoveAll(tmpDir)
		return nil, sfErr
	}
	stagedAsset := v.(*assets.StagedAsset)

	// Determine the final LocalPath this caller will receive:
	//   - leader: stagedAsset.LocalPath (= the singleflight's resolved
	//     file, which already exists inside THIS caller's tmpDir
	//     since the leader IS the goroutine that downloaded)
	//   - follower: copy to outputPath, then return outputPath
	leaderPath := stagedAsset.LocalPath
	finalLocalPath := leaderPath
	if leaderPath != outputPath {
		if cpErr := copyFileToPath(leaderPath, outputPath, s.svc.localFS); cpErr != nil {
			_ = s.svc.localFS.RemoveAll(tmpDir)
			return nil, fmt.Errorf("stock stager: copy concurrent download from %s to %s: %w", leaderPath, outputPath, cpErr)
		}
		finalLocalPath = outputPath
	}

	// Acquire AFTER the copy decision so the assetLeases side-map
	// key matches the LocalPath the caller will receive, and a copy
	// failure short-circuits WITHOUT acquiring a lease (avoids
	// orphan side-map entries — N1 fix auto-resolved by this
	// ordering).
	s.acquireSharedLease(cacheKey, leaderPath, finalLocalPath)

	return &assets.StagedAsset{
		LocalPath: finalLocalPath,
		Bytes:     stagedAsset.Bytes,
	}, nil
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

// Cleanup removes the staged file's parent temp directory AND
// releases the shared-lease refcount (if any).
//
// Two distinct paths:
//
//   - Leader caller (staged.LocalPath == lease.path): do NOT
//     remove ownDir directly — defer removal to releaseSharedLease
//     which unlinks ONLY when the LAST outstanding ref is released.
//     This is the load-bearing invariant that fixes the verdict's
//     race (B1): a premature Cleanup of one follower cannot unlink
//     the leader's tmp_dir out from under other concurrent followers
//     that may still be reading the file. Followers have already
//     copied-to-own-tmp per layer (a), so they're safe regardless,
//     but the lease keeps the LEADER's tmpDir alive for the
//     singleflight callback's own caller if it shares the path.
//
//   - Follower caller (staged.LocalPath != lease.path): the
//     follower's own tmpDir is independent of the leader's, so
//     remove ownDir directly + release one lease ref. The lease's
//     final unlink happens when the LAST ref (typically the
//     leader's) is released.
//
// godlike/07 honest-limitation: this is best-effort — a crashed
// process leaves refs on the lease. The cross-run cache (SQLite
// SourceCacheReader/Writer) is the persistent owner and re-downloads
// on next validation failure, so a leaked ref is recoverable via
// the cache invalidation path on the next pipeline run.
func (s *StockStager) Cleanup(_ context.Context, staged *assets.StagedAsset) error {
	if staged == nil || staged.LocalPath == "" {
		return nil
	}

	leaseKeyAny, hasLease := s.assetLeases.LoadAndDelete(staged.LocalPath)

	var ownErr error
	if hasLease {
		leaseKey, _ := leaseKeyAny.(string)
		// Detect "this caller IS the leader" — defer ownDir removal
		// to the lease so concurrent followers keep their access.
		if !s.isLeaseLeader(leaseKey, staged.LocalPath) {
			ownDir := filepath.Dir(staged.LocalPath)
			if ownDir != "" && ownDir != "." && ownDir != "/" {
				ownErr = s.svc.localFS.RemoveAll(ownDir)
			}
		}
		if rerr := s.releaseSharedLease(leaseKey); rerr != nil {
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

	// No lease (cache-hit or drive path): direct cleanup of ownDir.
	ownDir := filepath.Dir(staged.LocalPath)
	if ownDir == "" || ownDir == "." || ownDir == "/" {
		return nil
	}
	return s.svc.localFS.RemoveAll(ownDir)
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
// DriveReaderPort and writes it to outputPath. Returns a
// *StagedAsset pointing at the downloaded file on success.
// If the URL is a Drive folder, the folder is listed and the
// first video file is selected.
//
// godlike/07 typed-error contract: fileID/folderID extraction
// failure, unwired drive reader, Drive API errors, empty folder,
// and local I/O errors each surface as typed wraps (%w) so
// callers can errors.Is/As probe the underlying cause.
func (s *StockStager) stageFromDrive(ctx context.Context, ref assets.SourceRef, outputPath string) (*assets.StagedAsset, error) {
	if s.driveReader == nil {
		return nil, fmt.Errorf("stock stager: drive reader not wired (use WithDriveReader at composition time)")
	}

	fileID, fileErr := extractDriveFileID(ref.URL)
	if fileErr != nil || fileID == "" {
		// Not a file URL — try treating it as a folder URL.
		folderID := urlutil.FolderIDFromDriveLink(ref.URL)
		if folderID == "" {
			return nil, fmt.Errorf("stock stager: could not extract a Drive file or folder ID from %q", ref.URL)
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

	f, createErr := s.svc.localFS.Create(outputPath)
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

	fi, statErr := s.svc.localFS.Stat(outputPath)
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
