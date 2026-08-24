// Package artifacts — local_store.go (FASE 3-A, July 2026):
// concrete LocalStore implementing staging.Stager.
//
// godlike/06 SSOT (one canonical owner per fact): this file is
// the SOLE canonical owner of the FS-side implementation of the
// FASE 3 (a) "ArtifactStagingStore infrastrutturale" cut. The
// typed port lives in internal/application/assets/staging. FASE 3-C
// (a separate cut) wires the LocalStore into the per-artifact
// outbox pipeline + the SQLite StagesRepository (3-B).
//
// Step 7 split (July 2026): the canonical file holds the
// LocalStore concrete type + Config envelope + default consts +
// constructor + compile-time pin. The Stage method (the 10-step
// staging flow) lives in local_store_stage.go. The recovery flow
// (RecoverOrphans + workspaceTotalBytes) lives in
// local_store_recover.go. The 5 small private helpers
// (syscallStatfs + syncDirBestEffort + verifyPermission0700 +
// readFileSHAIfExists + ctxErr) live in local_store_helpers.go.
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
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/staging"
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
