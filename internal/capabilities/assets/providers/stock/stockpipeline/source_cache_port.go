// Package stockpipeline — source_cache_port.go
//
// Owns the cross-run source download cache surface:
//
//   - the application-layer ports (LocalFSPort, SourceCacheReader,
//     SourceCacheWriter, SourceCacheEntry) that StockStager uses to avoid
//     downloading the same YouTube/Drive video multiple times across pipeline
//     runs;
//   - the pre-claim warmer (WarmSourceCache + SourceWarmReport), which
//     materialises a job's known direct source URLs into that cache BEFORE a
//     worker claims the job, so the run's stock.stage_sources resolves from
//     cache instead of paying the yt-dlp download on its critical path.
//
// godlike/06 SSOT: the concrete implementation lives in
// internal/platform/sqlite/stocksourcecache.
// Composition root (wire_stock_pipeline.go) injects the concrete
// repository into the StockStager via WithSourceCache.
package stockpipeline

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// LocalFSPort is the Pattern 0 typed port for local filesystem I/O.
//
// godlike/07 PR-REFACTOR-P0-IO-BINDER (July 2026): the application
// layer MUST NOT import "os" or call os.* directly. Every file
// read/write/stat call goes through this port; the concrete
// implementation lives in internal/platform/filesystem and
// is injected via the SourceCacheDeps.LocalFS dependency at
// composition time.
//
// SSOT: this interface lives only in this package (the only
// consumer across the stock pipeline). Concrete implementations
// (filesystem.LocalAdapter) implement it structurally.
type LocalFSPort interface {
	// Stat returns the FileInfo for the named file.
	Stat(name string) (fs.FileInfo, error)
	// Open opens the named file for reading.
	Open(name string) (io.ReadCloser, error)
	// Create creates or truncates the named file for writing.
	Create(name string) (io.WriteCloser, error)
	// MkdirTemp creates a new temporary directory and returns its path.
	MkdirTemp(dir, pattern string) (string, error)
	// Remove removes the named file or (empty) directory.
	Remove(name string) error
	// RemoveAll removes path and any children it contains.
	RemoveAll(path string) error
	// MkdirAll creates a directory along with any necessary parents.
	MkdirAll(path string, perm fs.FileMode) error
	// CreateTemp creates a new temporary file, returning its path
	// and a WriteCloser for writing. The caller must close the
	// returned WriteCloser before using the path for hashing.
	CreateTemp(dir, pattern string) (string, io.WriteCloser, error)
	// TempDir returns the default directory to use for temporary files.
	TempDir() string
}

// SourceCacheReader abstracts the read side of the source download cache.
// Concrete: stocksourcecache.Repository.
type SourceCacheReader interface {
	// GetByCacheKey returns the active cache entry for the given key.
	// Returns (nil, nil) on cache miss.
	GetByCacheKey(ctx context.Context, cacheKey string) (*SourceCacheEntry, error)
}

// SourceCacheWriter abstracts the write side of the source download cache.
// Concrete: stocksourcecache.Repository.
type SourceCacheWriter interface {
	// Upsert inserts or replaces a cache entry.
	Upsert(ctx context.Context, entry *SourceCacheEntry) error
	// Invalidate marks a cache entry as invalid (e.g., file missing).
	Invalidate(ctx context.Context, cacheKey string) error
}

// SourceCacheEntry is the application-layer DTO for a cached source
// download. It mirrors stocksourcecache.CacheEntry but lives in the
// application package so the stockpipeline package does not import
// infrastructure.
type SourceCacheEntry struct {
	CacheKey        string
	Provider        string
	ExternalID      string
	SourceURL       string
	LocalPath       string
	FileSize        int64
	LegacyFileMD5   string
	DownloadSection string
	MergeFormat     string
	ForceKeyframes  bool
}

// validateCacheHit checks that a cached file still exists on disk and
// has a non-zero size. Returns nil on valid hit, error on invalid.
//
// The LocalFSPort is the Pattern 0 typed port (PR-REFACTOR-P0-IO-BINDER);
// nil is fail-closed — without an FS port the cache cannot validate
// any entry and the caller falls through to the download path.
func validateCacheHit(entry *SourceCacheEntry, fs LocalFSPort, log *zap.Logger) error {
	if entry == nil {
		return fmt.Errorf("cache entry is nil")
	}
	if entry.LocalPath == "" {
		return fmt.Errorf("cache entry has empty local_path")
	}
	if fs == nil {
		return fmt.Errorf("cache validation: LocalFSPort not wired (composition root must inject filesystem.NewLocal())")
	}
	fi, err := fs.Stat(entry.LocalPath)
	if err != nil {
		if log != nil {
			log.Warn("stock source cache: file missing on disk",
				zap.String("cache_key", entry.CacheKey),
				zap.String("local_path", entry.LocalPath),
				zap.Error(err))
		}
		return fmt.Errorf("cached file missing: %w", err)
	}
	if fi.Size() == 0 {
		if log != nil {
			log.Warn("stock source cache: file is zero bytes",
				zap.String("cache_key", entry.CacheKey),
				zap.String("local_path", entry.LocalPath))
		}
		return fmt.Errorf("cached file is zero bytes")
	}
	if fi.Size() != entry.FileSize {
		if log != nil {
			log.Warn("stock source cache: file size mismatch",
				zap.String("cache_key", entry.CacheKey),
				zap.Int64("expected", entry.FileSize),
				zap.Int64("actual", fi.Size()))
		}
		return fmt.Errorf("cached file size mismatch: expected %d, got %d", entry.FileSize, fi.Size())
	}
	return nil
}

// copyFileToPath copies srcPath to dstPath. Used by the cache to
// stage a cached file into a new temp directory without holding the
// original locked.
//
// The LocalFSPort is the Pattern 0 typed port (PR-REFACTOR-P0-IO-BINDER);
// nil is fail-closed so callers surface the wiring gap instead of
// silently returning a partial file.
//
// io.Copy handles the 32KB buffer + EOF semantics internally; the
// close-then-Close-error return pattern preserves the explicit
// flush the previous implementation provided.
func copyFileToPath(srcPath, dstPath string, fs LocalFSPort) error {
	if fs == nil {
		return fmt.Errorf("cache copy: LocalFSPort not wired (composition root must inject filesystem.NewLocal())")
	}
	src, err := fs.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := fs.Create(dstPath)
	if err != nil {
		return err
	}

	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return err
	}
	return dst.Close()
}

// ── Pre-claim source cache warming ───────────────────────────────────
//
// PR-STOCK-PRECLAIM-SOURCE-WARM (September 2026).
//
// Live job telemetry shows stock.stage_sources is the pipeline's single
// largest cost: on a cold cross-run cache every direct source pays a full
// yt-dlp download ON the run's critical path (12–90s per source observed;
// ~50–85% of the run wall). Direct URLs are already known at SUBMIT time,
// though, so their bytes can be materialised into the cross-run source cache
// BEFORE a worker claims the job — which turns the later in-run stage into a
// cache HIT and removes the download from the critical path entirely.
//
// Scope: only DirectURLs can be warmed ahead of claim. Search-resolved sources
// do not exist until resolveInputQueries runs inside the claimed run, so they
// keep the on-demand download path (the in-run staging fan-out).
//
// godlike/07 contract: warming is best-effort and never affects the job it
// warms. A source that fails to warm is counted + reported and skipped; it
// never fails submission, and the run falls back to its normal download path.
//
// godlike/06 SSOT: warming reuses the canonical staging path
// (stagerForRun → StockStager.stageSource) rather than a parallel download
// implementation. The cache key and the persisted LocalPath are therefore
// byte-identical to what the warmed run will look up — warming cannot create a
// cache entry the run is unable to hit.

// maxSourceWarmWorkers bounds concurrent pre-claim source downloads. Warming
// is network/process bound (yt-dlp), so the pool is deliberately small and
// independent from the in-run staging/extract parallelism
// (RuntimeConfig.MaxConcurrentJobs).
const maxSourceWarmWorkers = 3

// preClaimSourceWarmBudget bounds one warming pass. It matches the canonical
// acquisition per-call download default (10 minutes) so a slow source is not
// cut off mid-download by the warmer; the pass runs detached from the HTTP
// request that triggered it, so this never extends a response's lifetime.
const preClaimSourceWarmBudget = 10 * time.Minute

// warmPolicyVersion stamps the warming SourceRef so the acquisition stager
// resolves a deterministic stage id + cleanup token. What makes the later
// in-run stage a HIT is the cross-run source cache key
// (DeriveSourceCacheKey — URL + section + format + force, i.e. no policy
// version), which is identical on both paths; the policy stamp only keeps the
// warming call reproducible across retries.
const warmPolicyVersion = "v1"

// SourceWarmReport is the outcome of one pre-claim warming pass.
//
// Requested counts the candidate URLs after dedupe (Drive URLs and blanks are
// excluded — see WarmSourceCache). AlreadyCached counts sources that were
// already present AND valid in the cross-run cache (no work needed).
// Warmed + AlreadyCached + Failed always equals Requested.
type SourceWarmReport struct {
	Requested     int
	Warmed        int
	AlreadyCached int
	Failed        int
	// SkippedDrive counts Drive URLs dropped from the candidate set. Warming
	// them would download the full Drive file only to see its cache entry
	// invalidated by the stager's own temp-dir release, so they are excluded
	// by design (see WarmSourceCache).
	SkippedDrive int
	// Errors maps a failed source URL to the reason it could not be warmed.
	Errors map[string]string
}

// WarmSourceCache stages every direct source URL into the cross-run source
// cache so that a later stock.stage_sources call for the same URL is a cache
// HIT instead of a yt-dlp download. It is safe to call concurrently and is
// designed to be invoked before a worker claims the job.
//
// Behaviour:
//   - URLs are trimmed, deduplicated (blank entries dropped) and Drive URLs
//     are excluded (see SourceWarmReport.SkippedDrive).
//   - Sources already valid in the cross-run cache are skipped without any
//     download.
//   - The remaining sources are staged through the canonical path in a bounded
//     pool of maxSourceWarmWorkers workers. Each staged copy is released
//     immediately: the goal is to populate the cross-run cache, not to keep a
//     per-call copy alive.
//   - A source that fails to warm is reported in Errors and skipped. This
//     function never returns an error: it cannot make a job fail.
//
// A nil service (or a service with no cache/stager wired) returns an empty
// report — warming is an optimisation, never a hard dependency of submission.
func (s *Service) WarmSourceCache(ctx context.Context, urls []string) SourceWarmReport {
	report := SourceWarmReport{Errors: map[string]string{}}
	if s == nil {
		return report
	}
	candidates, skippedDrive := warmCandidateURLs(urls)
	report.SkippedDrive = skippedDrive
	report.Requested = len(candidates)
	if len(candidates) == 0 {
		return report
	}

	stager := s.stagerForRun()
	if stager == nil {
		for _, url := range candidates {
			report.Errors[url] = "stock source stager not wired"
		}
		report.Failed = len(candidates)
		return report
	}

	// Skip sources that are already valid in the cross-run cache: copying them
	// into a fresh temp dir just to release it again is pure overhead.
	pending := make([]string, 0, len(candidates))
	for _, url := range candidates {
		if s.sourceCacheHit(ctx, url) {
			report.AlreadyCached++
			continue
		}
		pending = append(pending, url)
	}
	if len(pending) == 0 {
		return report
	}

	outcomes := concurrent.ParallelMap(pending, maxSourceWarmWorkers, func(_ int, url string) warmOutcome {
		return s.warmOneSource(ctx, stager, url)
	})

	for idx, url := range pending {
		if outcomes[idx].err != nil {
			report.Failed++
			report.Errors[url] = outcomes[idx].err.Error()
			continue
		}
		report.Warmed++
	}
	return report
}

// warmOutcome is the per-source result of one warming attempt.
type warmOutcome struct {
	err error
}

// warmCandidateURLs trims + deduplicates the requested URLs, drops blanks and
// excludes Drive links (the second return value counts them).
//
// Drive sources are excluded on purpose. The Drive branch of
// StockStager.stageSource caches the temp file it writes under the caller's
// staging temp dir; releasing that stage (which the warmer must do) removes
// the directory, so the cache entry would be immediately invalid. Warming a
// Drive URL therefore downloads the whole file for nothing, and the later run
// would still re-download it.
func warmCandidateURLs(urls []string) ([]string, int) {
	if len(urls) == 0 {
		return nil, 0
	}
	candidates := make([]string, 0, len(urls))
	seen := make(map[string]struct{}, len(urls))
	skippedDrive := 0
	for _, raw := range urls {
		url := strings.TrimSpace(raw)
		if url == "" {
			continue
		}
		if _, dup := seen[url]; dup {
			continue
		}
		seen[url] = struct{}{}
		if isDriveURL(url) {
			skippedDrive++
			continue
		}
		candidates = append(candidates, url)
	}
	return candidates, skippedDrive
}

// sourceCacheHit reports whether the canonical cross-run cache already holds a
// VALID copy of the source. A stale entry (file missing / size mismatch) is
// not a hit — the source must be warmed again so the run does not fall back to
// a download.
func (s *Service) sourceCacheHit(ctx context.Context, url string) bool {
	if s == nil || s.sourceCacheReader == nil || s.localFS == nil {
		return false
	}
	entry, err := s.sourceCacheReader.GetByCacheKey(ctx, DeriveSourceCacheKey(url, "", "", false))
	if err != nil || entry == nil {
		return false
	}
	return validateCacheHit(entry, s.localFS, s.log) == nil
}

// warmOneSource stages one source into the cross-run cache through the
// canonical StockStager path and releases the caller's copy.
//
// The staged file returned by the acquisition stager is persistent and owned
// by the cross-run cache (StockStager only removes temp dirs it created
// itself), so releasing here leaves the cache entry valid for the claimed run.
func (s *Service) warmOneSource(ctx context.Context, stager acquisition.SourceStager, url string) warmOutcome {
	ref := acquisition.SourceRef{URL: url, PolicyVersion: warmPolicyVersion}
	prepared, err := stager.Prepare(ctx, acquisition.PrepareRequest{
		Source:         ref,
		IdempotencyKey: "stock.warm." + acquisition.DeriveIdempotencyKey(ref),
		CallerRef:      "stock.warmSourceCache",
	})
	if err != nil {
		if s.log != nil {
			s.log.Warn("stock source warm: prepare failed — the run will download on demand",
				zap.String("source_url", url),
				zap.Error(err))
		}
		return warmOutcome{err: fmt.Errorf("warm source %q: %w", url, err)}
	}
	if prepared == nil {
		return warmOutcome{err: fmt.Errorf("warm source %q: nil prepare context", url)}
	}

	// Release the caller's stage immediately: the cross-run cache keeps the
	// persistent path, so the warmed job still hits it. A release failure is
	// logged but NOT reported as a warm failure — the cache entry is already
	// populated and usable.
	if prepared.CleanupToken != "" {
		if relErr := stager.Release(ctx, prepared.CleanupToken); relErr != nil && s.log != nil {
			s.log.Warn("stock source warm: release of the caller's stage failed (cache entry stays valid)",
				zap.String("source_url", url),
				zap.Error(relErr))
		}
	}
	if s.log != nil {
		s.log.Info("stock source warm: source materialised into the cross-run cache",
			zap.String("source_url", url),
			zap.Int64("bytes", prepared.SizeBytes))
	}
	return warmOutcome{}
}
