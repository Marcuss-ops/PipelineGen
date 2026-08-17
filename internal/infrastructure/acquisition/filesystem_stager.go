// Package acquisition — filesystem_stager.go (Stock Cutover §12-4, July 2026).
//
// FilesystemStager is the canonical concrete for the
// `acquisition.SourceStager` port. It is INFRASTRUCTURE-LEVEL
// (lives under `internal/infrastructure/acquisition/`) per
// AGENTS.md Pattern 0: the port lives in application; the
// concrete lives in infrastructure.
//
// Persistence model (per the §12-4 design summary):
//
//   - Staged files live at {stagingRoot}/{DeriveStageID(ref)} —
//     a stable path-based key so repeat Prepare calls land on the
//     same file (idempotency via filename, no DB lookup needed).
//   - A sibling {stagingRoot}/{DeriveStageID(ref)}.meta.json carries
//     the PrepareContext (SourceRef, SHA256, SizeBytes, MIMEType,
//     ExpiresAt, CleanupToken). The metadata is the canonical
//     source of truth for downstream consumers (a stat-record
//     is auxiliary; the JSON sidecar is the contract).
//   - On Prepare: if the {stagingRoot}/{ID} file already exists AND
//     its {ID}.meta.json SHA matches the new request's content SHA
//     → return the existing PrepareContext (natural idempotency).
//     ELSE download fresh (caller-supplied `Fetch` closure),
//     atomically rename into place, write `.meta.json`.
//   - On Release: validate CleanupToken against the .meta.json
//     contents; matched → remove both files; mismatched → typed error.
//
// Why filesystem + JSON sidecar (NOT SQLite)?
//   - §12-4 scope: simpler to ship + test + verify; no schema
//     migration overhead.
//   - Cross-process safety: the atomic rename pattern guarantees
//     only one writer wins (the loser sees the established file).
//   - Forward-pointer §12-4.2: if multi-replica staging requires
//     shared visibility, lift the registry to a SQLite table
//     while keeping the FS storage for content (the cheap-to-move
//     half).
//
// The concrete is BUILT to be the sbStager for production; it
// accepts a `Fetch(ctx, req, dstPath) error` closure that does the
// actual byte fetch (yt-dlp subprocess, HTTP GET, Drive download,
// etc.). This keeps FilesystemStager free of any downloader /
// transport dependency; the production YTDLPSourceStager is
// another concrete (forward-pointer; see §12-4.2) that wraps the
// existing `*downloader.YTDLPDownloader`.

package acquisition

import (
	"context"
	"fmt"
	"os"
	"sync"

	"go.uber.org/zap"

	appacq "github.com/Marcuss-ops/PipelineGen/internal/application/acquisition"
)

// FetchFn is the byte-source closure the FilesystemStager calls to
// download the staged content. Concrete callers (yt-dlp, HTTP,
// Drive, ...) wire this from their own transport.
//
// dstPath is the FINAL canonical path (FilesystemStager has already
// created {dstPath}.partial and expects the closure to write bytes
// there). Success → FilesystemStager atomically renames
// {dstPath}.partial → {dstPath}. The closure is responsible for
// returning a NON-NIL error if the download failed; FilesystemStager
// cleans up the .partial file in that case.
//
// The onWireSHA256 callback (if non-nil) is invoked with the SHA256
// of the bytes as the closure wrote them — this lets the concrete
// verify the download matches the upstream's declared hash (used by
// future yt-dlp + content-stable Addressing paths).
type FetchFn func(ctx context.Context, req appacq.PrepareRequest, dstPath string, onWireSHA256 func(hashHex string)) error

// FilesystemStager is the canonical FS-backed SourceStager concrete.
// It is safe for concurrent use by uploaders + the broker's
// per-worker claim goroutines (RequestID lookups are mutex-protected).
type FilesystemStager struct {
	stagingRoot string
	fetch       FetchFn
	log         *zap.Logger

	mu    sync.Mutex
	byTok map[string]appacq.PrepareContext // cache: CleanupToken → in-memory PrepareContext

	// prepareLocks serializes Prepare calls for the same stage ID while
	// allowing unrelated sources to download concurrently. Without this
	// keyed lock, concurrent callers share the same `.partial` path and
	// their yt-dlp processes race while renaming the temporary output.
	prepareLocksMu sync.Mutex
	prepareLocks   map[string]*prepareLockEntry
}

// Options bundles construction-time configuration for FilesystemStager.
// StagingRoot is REQUIRED (where files live); Fetch is REQUIRED (how
// bytes arrive); Log is optional (zap.NewNop default).
type Options struct {
	StagingRoot string
	Fetch       FetchFn
	Log         *zap.Logger
}

// NewFilesystemStager constructs the canonical FS-backed SourceStager.
// StagingRoot is created (MkdirAll) if missing. Fetch is called per
// Prepare that does NOT find a cached staged file.
//
//	// Compile-time assertion: *FilesystemStager satisfies the canonical
//
// application-layer SourceStager port. Drift between the concrete
// signature and the port surface is a build-time failure rather
// than a runtime panic.
var _ appacq.SourceStager = (*FilesystemStager)(nil)

// NewFilesystemStager wires a fully-ready FilesystemStager. The
// returned concrete is ready for Prepare / Release; stagingRoot is
// auto-created at construction so the first call doesn't pay the
// MkdirAll cost on the hot path.
func NewFilesystemStager(opts Options) (*FilesystemStager, error) {
	if opts.StagingRoot == "" {
		return nil, fmt.Errorf("acquisition.NewFilesystemStager: StagingRoot is required")
	}
	if opts.Fetch == nil {
		return nil, fmt.Errorf("acquisition.NewFilesystemStager: Fetch is required (the byte-source closure must be supplied by the caller)")
	}
	if err := os.MkdirAll(opts.StagingRoot, 0o755); err != nil {
		return nil, fmt.Errorf("acquisition.NewFilesystemStager: create staging root %q: %w", opts.StagingRoot, err)
	}
	if opts.Log == nil {
		opts.Log = zap.NewNop()
	}
	return &FilesystemStager{
		stagingRoot:  opts.StagingRoot,
		fetch:        opts.Fetch,
		log:          opts.Log,
		byTok:        make(map[string]appacq.PrepareContext),
		prepareLocks: make(map[string]*prepareLockEntry),
	}, nil
}

// ── Cache plumbing ──────────────────────────────────────────────────

func (f *FilesystemStager) cacheToken(token string, ctx appacq.PrepareContext) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byTok[token] = ctx
}

func (f *FilesystemStager) forgetToken(token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.byTok, token)
}

type prepareLockEntry struct {
	mu   sync.Mutex
	refs int
}

func (f *FilesystemStager) acquirePrepareLock(stageID string) func() {
	f.prepareLocksMu.Lock()
	if f.prepareLocks == nil {
		f.prepareLocks = make(map[string]*prepareLockEntry)
	}
	entry := f.prepareLocks[stageID]
	if entry == nil {
		entry = &prepareLockEntry{}
		f.prepareLocks[stageID] = entry
	}
	entry.refs++
	f.prepareLocksMu.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()

		f.prepareLocksMu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(f.prepareLocks, stageID)
		}
		f.prepareLocksMu.Unlock()
	}
}
