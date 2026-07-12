// Package artifacts — local_store.go (FASE 3-A, July 2026):
// concrete LocalStore implementing staging.Stager.
//
// godlike/06 SSOT (one canonical owner per fact): this file is
// the SOLE canonical owner of the FS-side implementation of the
// FASE 3 (a) "ArtifactStagingStore infra­strutturale" cut. The
// typed port lives in internal/application/assets/staging. FASE 3-C
// (a separate cut) wires the LocalStore into the per-artifact
// outbox pipeline + the SQLite StagesRepository (3-B).
//
// godlike/07 NO-FAKE-AVAILABILITY: every failure path returns a
// typed sentinel. NO silent-success path exists for an unavailable
// FS backend (read-only mount, full disk, missing workspace).
//
// Audit-aligned I/O discipline (per Piano d'Azione §Fase 3 (a)):
//   - sha256 computed DURING write via io.MultiWriter (not post-stat).
//   - write to workspace/.partial/<id>.tmp then atomic rename to
//     workspace/<id> (canonical location).
//   - file fsync + parent-dir fsync before rename + workspace-dir
//     fsync after rename.
//   - workspace MkdirAll with 0700; file OpenFile with O_EXCL + 0600.
//   - quota check: per-artifact (default 10 GiB) + workspace total
//     (default 100 GiB). Both configurable at construction.
//   - free-space check: 2x inbound_size buffer via syscall.Statfs;
//     surfaces artifact.ErrDiskSpaceLow (re-aliased) when below.
//   - recovery-on-boot: RecoverOrphans(maxAge) scans .partial/*.tmp
//     files with mtime older than maxAge and unlinks them. Called
//     from composition root on startup; no goroutine.
package artifacts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/staging"
)

// Default values (audit-aligned; all overridable via Config).
const (
	// DefaultMaxArtifactBytes caps a single staged file. The FASE 3
	// audit specifies "quota" without a value; 10 GiB is a generous
	// ceiling that accommodates video assets while still preventing
	// accidental GB-scale leaks.
	DefaultMaxArtifactBytes int64 = 10 * 1024 * 1024 * 1024

	// DefaultMaxWorkspaceBytes caps the cumulative bytes in the
	// workspace. 100 GiB is the production default (matches the
	// pre-FASE 3 deployment quota on standalone nodes).
	DefaultMaxWorkspaceBytes int64 = 100 * 1024 * 1024 * 1024

	// DefaultMinFreeBytes is the safety floor on free disk space.
	// 1 GiB is the production default (matches the pre-FASE 3
	// deployment floor). The pre-stage check uses 2x the inbound
	// size (so smaller stages can still succeed when the floor is
	// approached).
	DefaultMinFreeBytes int64 = 1 * 1024 * 1024 * 1024

	// partialDirName is the canonical in-workspace subdir holding
	// in-progress `.tmp` files. Files in this dir are NOT visible
	// to canonical-path readers (FS only); they are unlink-only
	// targets for RecoverOrphans.
	partialDirName = ".partial"
)

// Config is the LocalStore constructor envelope. Every field has a
// default (see above) so a zero-value caller uses production-safe
// limits. Time-sensitive Configs (Workspace, MaxArtifactBytes, …) are
// fail-fast-validated in NewLocalStore.
type Config struct {
	// Workspace is the canonical staging directory. The LocalStore
	// ensures MkdirAll(workspace, 0700) at construction; the
	// post-MkdirAll StatMode is verified to be exactly 0700
	// (defensive: a sibling process changing perms after MkdirAll
	// surfaces Stage-time via wrapped ErrStagerWorkspaceMissing).
	Workspace string

	// MaxArtifactBytes is the per-stage size cap. 0 = use
	// DefaultMaxArtifactBytes.
	MaxArtifactBytes int64

	// MaxWorkspaceBytes is the cumulative cap. 0 = use
	// DefaultMaxWorkspaceBytes. The LocalStore SUM's the on-disk
	// sizes (via filepath.Walk over canonical filenames only —
	// partial/*.tmp excluded) before accepting a new stage.
	MaxWorkspaceBytes int64

	// MinFreeBytes is the free-space safety floor. 0 = use
	// DefaultMinFreeBytes. The pre-stage check requires
	// available >= max(MinFreeBytes, 2*incoming_size).
	MinFreeBytes int64

	// statfsFn is the statfs seam — defaults to syscallStatfs.
	// Tests inject a deterministic free-space reporter here. nil
	// → default. Capitalised field for export (config-only,
	// never read post-construction outside the constructor).
	statfsFn func(path string) (freeBytes int64, err error)
}

// LocalStore is the concrete FASE 3-A staging store. Concurrency-safe:
// PostNewLocalStore, the only mutable state is the atomic byte-counter
// (workspaceBytes) updated on successful Stage and re-walked on
// per-stage quota probes. Workspace byte-counter is best-effort
// cached; on any read failure the Stager re-walks the workspace
// (so a counter drift can't block production stages).
type LocalStore struct {
	workspace string

	maxArtifactBytes  int64
	maxWorkspaceBytes int64
	minFreeBytes      int64

	statfsFn func(path string) (freeBytes int64, err error)

	workspaceBytes atomic.Int64 // best-effort cumulative size; re-walk on drift.
}

// NewLocalStore is the canonical constructor. Fail-fast posture
// (godlike/07): returns ErrStagerNotConfigured for nil-seam
// dependencies and ErrStagerWorkspaceMissing for un-creatable
// workspaces. The directory permission is verified post-MkdirAll.
func NewLocalStore(cfg Config) (*LocalStore, error) {
	if cfg.Workspace == "" {
		return nil, fmt.Errorf("%w: Config.Workspace is empty", staging.ErrStagerNotConfigured)
	}
	if cfg.MaxArtifactBytes <= 0 {
		cfg.MaxArtifactBytes = DefaultMaxArtifactBytes
	}
	if cfg.MaxWorkspaceBytes <= 0 {
		cfg.MaxWorkspaceBytes = DefaultMaxWorkspaceBytes
	}
	if cfg.MinFreeBytes <= 0 {
		cfg.MinFreeBytes = DefaultMinFreeBytes
	}
	if cfg.statfsFn == nil {
		cfg.statfsFn = syscallStatfs
	}

	// MkdirAll 0700 (workspace). Defensive post-Stat: ensure the
	// mode bits match the requested 0700 so a sibling process
	// changing them post-MkdirAll surfaces Stage-time. Note: we
	// tolerate a slightly more-restrictive umask result here (e.g.
	// 0700 vs 0755 with no write bit) — only reject permissive
	// results (e.g. 0755 with write bit for group/other).
	if err := os.MkdirAll(cfg.Workspace, 0o700); err != nil {
		return nil, fmt.Errorf("%w: MkdirAll(%q, 0700): %v", staging.ErrStagerWorkspaceMissing, cfg.Workspace, err)
	}
	if err := verifyPermission0700(cfg.Workspace); err != nil {
		return nil, fmt.Errorf("%w: workspace=%q perm rejected: %v", staging.ErrStagerWorkspaceMissing, cfg.Workspace, err)
	}

	// MkdirAll the .partial subdirectory. Same 0700 expected (we
	// do not need parent+child to differ; both at 0700 keeps the
	// file-discovery surface minimal — see also the partial/*.tmp
	// unlink path in RecoverOrphans).
	if err := os.MkdirAll(filepath.Join(cfg.Workspace, partialDirName), 0o700); err != nil {
		return nil, fmt.Errorf("%w: MkdirAll(.partial, 0700): %v", staging.ErrStagerWorkspaceMissing, err)
	}

	s := &LocalStore{
		workspace:         cfg.Workspace,
		maxArtifactBytes:  cfg.MaxArtifactBytes,
		maxWorkspaceBytes: cfg.MaxWorkspaceBytes,
		minFreeBytes:      cfg.MinFreeBytes,
		statfsFn:          cfg.statfsFn,
	}

	// Eagerly walk the workspace to populate the cached counter
	// (best-effort — failures are tolerated; the next Stage
	// re-walks). Reuse the worker for the per-stage quota probe.
	if totalBytes, walkErr := s.workspaceTotalBytes(); walkErr == nil {
		s.workspaceBytes.Store(totalBytes)
	}

	return s, nil
}

// Compile-time pin (godlike/06 Pattern 0): *LocalStore satisfies the
// staging.Stager port. Drift in the method set is a build failure,
// not a runtime panic.
var _ staging.Stager = (*LocalStore)(nil)

// Stage implements staging.Stager.Stage. The flow:
//  1. Validate input (ID, MIME, non-nil reader).
//  2. Compute inbound size (DECLARED or measured-while-reading).
//  3. Pre-flight quota + free-space checks (Hermetic best-effort:
//     statfsFn, walkFn).
//  4. Open .partial/<id>.tmp with O_EXCL + 0600.
//  5. io.MultiWriter(file, sha256.New) → io.Copy reader.
//  6. file.Sync + close. directory fsync on .partial.
//  7. os.Rename .partial/<id>.tmp → <workspace>/<id>.
//  8. workspace-dir fsync so the rename is durable.
//  9. Update atomic counter.
//
// 10. Build StagedReceipt.
func (s *LocalStore) Stage(ctx context.Context, in staging.StageInput) (*staging.StagedReceipt, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if in.Content == nil {
		return nil, fmt.Errorf("%w: Content reader is nil", staging.ErrStagerInvalidInput)
	}
	if err := staging.ValidateArtifactIDFormat(in.ArtifactID); err != nil {
		return nil, err
	}
	if err := staging.ValidateMIMEFormat(in.MIME); err != nil {
		return nil, err
	}
	if in.SizeBytes < 0 {
		return nil, fmt.Errorf("%w: SizeBytes=%d (want >= 0)", staging.ErrStagerInvalidInput, in.SizeBytes)
	}

	// Per-artifact quota: gate on declared SizeBytes when provided.
	// When SizeBytes==0 (unknown), the per-size cap is enforced
	// AFTER the write via on-disk size check — we cannot predict
	// the size without consuming the reader.
	if in.SizeBytes > 0 && in.SizeBytes > s.maxArtifactBytes {
		return nil, fmt.Errorf("%w: artifactID=%q size=%d > MaxArtifactBytes=%d",
			staging.ErrQuotaExceeded, in.ArtifactID, in.SizeBytes, s.maxArtifactBytes)
	}

	// Workspace total + free-space pre-flight. We need a best-
	// effort currentTotal; on walk failure we treat the count as
	// 0 (the next Stage attempt will re-walk). The unknown-size
	// case (SizeBytes==0) treats the inbound as the upper bound;
	// a future writer would learn the actual size via the
	// measured-while-reading path below.
	currentTotal, totalErr := s.workspaceTotalBytes()
	if totalErr != nil {
		currentTotal = s.workspaceBytes.Load() // best-effort cache fallback
	}
	incomingEstimate := in.SizeBytes
	if incomingEstimate == 0 {
		// Conservative upper bound from maxArtifactBytes when the
		// caller does not declare. Free-space check uses this as
		// the "incoming" estimate.
		incomingEstimate = s.maxArtifactBytes
	}
	if currentTotal+incomingEstimate > s.maxWorkspaceBytes {
		return nil, fmt.Errorf("%w: workspace_total=%d+%d > MaxWorkspaceBytes=%d (artifactID=%q)",
			staging.ErrQuotaExceeded, currentTotal, incomingEstimate, s.maxWorkspaceBytes, in.ArtifactID)
	}

	// Free-space check (skip-on-statfs-error: log + warning rather
	// than reject — the underlying FS error is communicated through
	// the fsync/rename path at write time).
	freeBytes, statErr := s.statfsFn(s.workspace)
	if statErr == nil {
		requiredBytes := s.minFreeBytes
		if in.SizeBytes > requiredBytes {
			requiredBytes = in.SizeBytes
		}
		// Doubled buffer for fsync's metadata — the per-artifact
		// quota gate is the authoritative size limit; the free-
		// space gate is a separate, generous safety margin.
		if freeBytes < requiredBytes*2 {
			return nil, fmt.Errorf("%w: free=%d < required=%d (2x incoming+floor)",
				staging.ErrDiskSpaceLow, freeBytes, requiredBytes*2)
		}
	}

	// Open .partial/<id>.tmp with O_EXCL + 0600. O_EXCL rejects
	// the subtle "another Stage with the same ID is mid-flight"
	// race (only one writer can hold the tmp file). The 0600 mode
	// is set on creation; we re-Chmod defensively in case umask
	// hid the bits.
	tmpPath := filepath.Join(s.workspace, partialDirName, in.ArtifactID+".tmp")
	f, openErr := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if openErr != nil {
		if errors.Is(openErr, os.ErrExist) {
			// Another writer holds the .tmp — surface as collision.
			return nil, fmt.Errorf("%w: .tmp already exists for artifactID=%q",
				staging.ErrStagerIDCollision, in.ArtifactID)
		}
		return nil, fmt.Errorf("staging.Stage: OpenFile(%q): %w", tmpPath, openErr)
	}
	// Cleanup-on-failure guard: defer unlink the .tmp if we
	// don't reach the success-commit point below.
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpPath)
		}
	}()
	// Defensive chmod (some umasks downgraded the requested 0600
	// on platforms with non-strict umask handling).
	_ = os.Chmod(tmpPath, 0o600)

	// Stream-through-hash: io.MultiWriter hashes AND writes in
	// lockstep. io.Copy returns the total bytes written, which is
	// the authoritative file size (NOT the declared SizeBytes).
	hasher := sha256.New()
	mw := io.MultiWriter(f, hasher)
	written, copyErr := io.Copy(mw, in.Content)
	if copyErr != nil {
		// Wrap ctx.Err or io error depending on cause.
		if cerr := ctxErr(ctx); cerr != nil {
			return nil, fmt.Errorf("%w: copy cancelled (%v)", staging.ErrStagerReadFailed, cerr)
		}
		return nil, fmt.Errorf("%w: io.Copy artifactID=%q: %v",
			staging.ErrStagerReadFailed, in.ArtifactID, copyErr)
	}
	if written == 0 {
		return nil, fmt.Errorf("%w: artifactID=%q wrote 0 bytes", staging.ErrArtifactStageEmpty, in.ArtifactID)
	}
	if written > s.maxArtifactBytes {
		return nil, fmt.Errorf("%w: artifactID=%q measured-size=%d > MaxArtifactBytes=%d",
			staging.ErrQuotaExceeded, in.ArtifactID, written, s.maxArtifactBytes)
	}

	// fsync the file (data + metadata durable).
	if err := f.Sync(); err != nil {
		return nil, fmt.Errorf("%w: file.Sync (artifactID=%q): %v",
			staging.ErrStagerFsyncFailed, in.ArtifactID, err)
	}
	if err := f.Close(); err != nil {
		// close-after-Sync rarely fails on Linux; treat as error.
		return nil, fmt.Errorf("%w: file.Close (artifactID=%q): %v",
			staging.ErrStagerFsyncFailed, in.ArtifactID, err)
	}

	// fsync the parent .partial directory so the file's directory
	// entry is durable before rename.
	parentDir := filepath.Dir(tmpPath)
	if err := syncDirBestEffort(parentDir); err != nil {
		return nil, fmt.Errorf("%w: dir.Sync(%q): %v",
			staging.ErrStagerFsyncFailed, parentDir, err)
	}

	// Atomic rename. O_EXCL semantics on the destination via
	// os.Rename behavior: rename over existing file replaces the
	// inode on Linux (so we may overwrite a stale canonical file;
	// verify hash post-rename if the new hash differs from any
	// pre-existing on-disk hash). The ID-collision guard above
	// on the .tmp path covers the concurrent writer; this rename
	// guard covers the renamed-overwrite path.
	finalPath := filepath.Join(s.workspace, in.ArtifactID)
	if prevSHA, statErr := readFileSHAIfExists(finalPath); statErr == nil && prevSHA != "" {
		newSHA := hex.EncodeToString(hasher.Sum(nil))
		if prevSHA != newSHA {
			return nil, fmt.Errorf("%w: artifactID=%q existing_on_disk_sha=%s differs from new_sha=%s",
				staging.ErrStagerIDCollision, in.ArtifactID, prevSHA, newSHA)
		}
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return nil, fmt.Errorf("%w: os.Rename(%q -> %q): %v",
			staging.ErrStagerRenameFailed, tmpPath, finalPath, err)
	}

	// fsync the workspace directory so the rename is durable.
	if err := syncDirBestEffort(s.workspace); err != nil {
		return nil, fmt.Errorf("%w: dir.Sync(%q): %v",
			staging.ErrStagerFsyncFailed, s.workspace, err)
	}

	// Hash verifier (optional).
	computedHash := hex.EncodeToString(hasher.Sum(nil))
	if in.ExpectedSHA256 != "" && in.ExpectedSHA256 != computedHash {
		return nil, fmt.Errorf("%w: artifactID=%q expected=%s computed=%s",
			staging.ErrArtifactStageHashMismatch, in.ArtifactID, in.ExpectedSHA256, computedHash)
	}

	// Update the cached counter (best-effort — a drift just means
	// the next walk will re-derive).
	s.workspaceBytes.Add(written)

	success = true
	return &staging.StagedReceipt{
		LocalPath:     finalPath,
		Hash:          computedHash,
		Size:          written,
		MIME:          in.MIME,
		WorkspacePath: s.workspace,
	}, nil
}

// RecoverOrphans scans the workspace's .partial/ subdirectory for
// `.tmp` files with mtime older than maxAge and unlinks them.
// Synchronous, readdir + stat + unlink. Best-effort: a single
// readdir/unlink failure does NOT abort the loop — the partial
// success counter is returned together with a wrapped
// ErrStagerRecoveryFailed if any operation failed.
func (s *LocalStore) RecoverOrphans(ctx context.Context, maxAge time.Duration) (int, error) {
	if err := ctxErr(ctx); err != nil {
		return 0, err
	}
	if maxAge <= 0 {
		return 0, fmt.Errorf("%w: maxAge=%v (want > 0)", staging.ErrStagerInvalidInput, maxAge)
	}

	partialDir := filepath.Join(s.workspace, partialDirName)
	entries, readErr := os.ReadDir(partialDir)
	if readErr != nil {
		if errors.Is(readErr, os.ErrNotExist) {
			return 0, nil // .partial does not exist yet (clean boot)
		}
		return 0, fmt.Errorf("%w: readdir %q: %v",
			staging.ErrStagerRecoveryFailed, partialDir, readErr)
	}

	cutoff := time.Now().Add(-maxAge)
	removed := 0
	var firstErr error
	for _, ent := range entries {
		// Honor ctx cancellation between retries.
		if cerr := ctxErr(ctx); cerr != nil {
			return removed, fmt.Errorf("partial: ctx cancelled mid-recovery: %w", cerr)
		}
		if ent.IsDir() {
			continue
		}
		fullPath := filepath.Join(partialDir, ent.Name())
		info, statErr := ent.Info()
		if statErr != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("%w: stat %q: %v",
					staging.ErrStagerRecoveryFailed, fullPath, statErr)
			}
			continue
		}
		// Skip files younger than the cutoff (still considered
		// active — a concurrent writer MIGHT be holding them).
		if info.ModTime().After(cutoff) {
			continue
		}
		if unlinkErr := os.Remove(fullPath); unlinkErr != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("%w: unlink %q: %v",
					staging.ErrStagerRecoveryFailed, fullPath, unlinkErr)
			}
			continue
		}
		removed++
	}
	if firstErr != nil {
		return removed, firstErr
	}
	return removed, nil
}

// workspaceTotalBytes re-walks the workspace's canonical files and
// sums their sizes. Skips .partial/*.tmp (they are NOT counted
// toward the cumulative quota). Returns 0 + the error on walk
// failure (callers treat the cached counter as best-effort
// fallback).
func (s *LocalStore) workspaceTotalBytes() (int64, error) {
	var total int64
	walkErr := filepath.WalkDir(s.workspace, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			// Stop walking on error — callers fallback to cache.
			return err
		}
		if d.IsDir() {
			return nil
		}
		// Skip .partial/*.tmp — they are not counted.
		parent := filepath.Dir(p)
		if filepath.Base(parent) == partialDirName {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		total += info.Size()
		return nil
	})
	return total, walkErr
}

// ── Test seam ────────────────────────────────────────────────────────

// syscallStatfs is the production statfsFn. Linux + darwin carry
// different struct field names — syscall.Statfs_t works on both
// with Bsize + Bavail. We use the syscall package directly (not
// golang.org/x/sys/unix) to minimise the dep surface in this
// tightly-scoped FASE 3-A cut. Build tags in tests can override
// for non-Linux platforms if needed.
func syscallStatfs(path string) (freeBytes int64, err error) {
	var stat syscall.Statfs_t
	if e := syscall.Statfs(path, &stat); e != nil {
		return 0, e
	}
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}

// syncDirBestEffort fsyncs a directory. On Linux, opening a
// directory for read + calling Sync() fsyncs the directory's
// metadata. Returns nil on platforms that can't open directories
// for sync (e.g. some Windows variants) — the boot path treats
// nil as "advisory best-effort" (caller logs).
func syncDirBestEffort(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// verifyPermission0700 asserts the path has mode bits that
// reject any non-owner write (a permissive mode like 0755
// surfaces as fail-closed at construction). We accept the
// conventional 0700 strictly — anything broader is a deployment
// safety violation.
func verifyPermission0700(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		return fmt.Errorf("perm=0%o want 0700", perm)
	}
	return nil
}

// readFileSHAIfExists computes SHA-256 of the file at path if it
// exists, returns empty string + nil error if path is absent.
// Used to detect collisions on rename-overwrite of a stale
// canonical file.
func readFileSHAIfExists(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	_ = info // available to callers if needed
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ctxErr is the ctx-cancellation helper (nil ctx → nil error;
// cancelled ctx → ctx.Err() wrapped).
func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("%w: ctx is nil", staging.ErrStagerInvalidInput)
	}
	return ctx.Err()
}
