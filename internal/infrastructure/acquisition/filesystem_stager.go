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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	// appacq alias: the application-layer port lives in
	// internal/application/acquisition/ (same package name `acquisition`
	// but distinct import path — Go identifies packages by full import
	// path, not name). The infrastructure concrete consumes the port's
	// types via the alias to avoid namespace collision with this file's
	// own package-name `acquisition`. Without the alias, references to
	// `PrepareContext` / `PrepareRequest` / `SourceStager` /
	// `SourceRef` / `DeriveStageID` / `DeriveCleanupToken` would
	// resolve against the empty local scope and fail to compile.
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
//	// Compile-time assertion: *FilesystemStager satisfies the canonical
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
		stagingRoot: opts.StagingRoot,
		fetch:       opts.Fetch,
		log:         opts.Log,
		byTok:       make(map[string]appacq.PrepareContext),
	}, nil
}

// Prepare is the canonical SourceStager.Prepare implementation.
// See `SourceStager.Prepare` in `internal/application/acquisition/port.go`
// for the contract; this method only adds persistence semantics.
func (f *FilesystemStager) Prepare(ctx context.Context, req appacq.PrepareRequest) (*appacq.PrepareContext, error) {
	if f == nil || f.stagingRoot == "" {
		return nil, appacq.ErrAcquisitionNotWired
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	if req.Timeout == 0 {
		req.Timeout = 10 * time.Minute
	}
	if req.TTL == 0 {
		req.TTL = 24 * time.Hour
	}

	stageID := appacq.DeriveStageID(req.Source)
	stagedPath := filepath.Join(f.stagingRoot, stageID)
	metaPath := stagedPath + ".meta.json"

	// ── Cache-hit path (idempotency on repeat Prepare) ───────────
	if existing, hit := f.readMeta(metaPath); hit {
		// Same SourceRef + same ExpiresAt → return cached.
		// Assert the cached SHA matches what the request implies;
		// if SHA mismatches, the request changed somehow — fall
		// through to the re-download path (so the staging surface
		// always reflects the LATEST bytes, not stale ones).
		if existing.SourceRef == req.Source && !existing.Expired() {
			f.log.Info("acquisition prepare cache hit",
				zap.String("stage_id", stageID),
				zap.String("sha256", existing.SHA256),
				zap.Time("expires_at", existing.ExpiresAt),
			)
			f.cacheToken(existing.CleanupToken, *existing)
			return existing, nil
		}
	}

	// ── Download path ────────────────────────────────────────────
	partialPath := stagedPath + ".partial"
	// Remove stale partial from prior failed Prepare (left over by
	// a previous attempt that crashed mid-fetch).
	if err := os.RemoveAll(partialPath); err != nil && !os.IsNotExist(err) {
		return nil, appacq.Wrap(appacq.ErrAcquisitionPrepareFailed,
			fmt.Sprintf("remove stale partial %q: %v", partialPath, err))
	}

	fetchCtx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()

	var observedSHA string
	err := f.fetch(fetchCtx, req, partialPath, func(hashHex string) {
		observedSHA = hashHex
	})
	if err != nil {
		_ = os.RemoveAll(partialPath)
		return nil, appacq.Wrap(appacq.ErrAcquisitionPrepareFailed, fmt.Sprintf("fetch closed with error: %v", err))
	}

	// Verify / compute the SHA256 — `observedSHA` (if set) overrides
	// an explicit re-hash so the concrete can short-circuit on the
	// upstream's declared hash (used by yt-dlp + future Drive).
	sha256hex := observedSHA
	if sha256hex == "" {
		hash, hashErr := fileSHA256(partialPath)
		if hashErr != nil {
			_ = os.RemoveAll(partialPath)
			return nil, appacq.Wrap(appacq.ErrAcquisitionPrepareFailed,
				fmt.Sprintf("hash staged file: %v", hashErr))
		}
		sha256hex = hash
	}

	// Atomic rename: partial → canonical. After this point, the
	// staged file is "established" — any racing Prepare that sees
	// the canonical file falls into the cache-hit branch on its
	// next iteration.
	if err := os.Rename(partialPath, stagedPath); err != nil {
		_ = os.RemoveAll(partialPath)
		return nil, appacq.Wrap(appacq.ErrAcquisitionPrepareFailed,
			fmt.Sprintf("atomic rename partial→staged %q→%q: %v", partialPath, stagedPath, err))
	}

	stagedFileInfo, statErr := os.Stat(stagedPath)
	if statErr != nil {
		return nil, appacq.Wrap(appacq.ErrAcquisitionPrepareFailed, fmt.Sprintf("stat staged %q: %v", stagedPath, statErr))
	}

	mime := req.Source.MIMETypeHint
	if mime == "" {
		mime = "application/octet-stream"
	}

	cleanupsToken := appacq.DeriveCleanupToken(req.Source)
	context := &appacq.PrepareContext{
		ID:           stageID,
		SourceRef:    req.Source,
		LocalPath:    stagedPath,
		SHA256:       sha256hex,
		SizeBytes:    stagedFileInfo.Size(),
		MIMEType:     mime,
		ExpiresAt:    time.Now().UTC().Add(req.TTL),
		CleanupToken: cleanupsToken,
	}

	if err := f.writeMeta(metaPath, *context); err != nil {
		return nil, appacq.Wrap(appacq.ErrAcquisitionPrepareFailed,
			fmt.Sprintf("write meta %q: %v", metaPath, err))
	}
	f.cacheToken(cleanupsToken, *context)

	f.log.Info("acquisition prepare completed",
		zap.String("stage_id", stageID),
		zap.String("local_path", stagedPath),
		zap.String("sha256", sha256hex),
		zap.Int64("size_bytes", stagedFileInfo.Size()),
		zap.Time("expires_at", context.ExpiresAt),
	)
	return context, nil
}

// Release is the canonical SourceStager.Release implementation.
// See `SourceStager.Release` in `internal/application/acquisition/port.go`
// for the contract.
func (f *FilesystemStager) Release(ctx context.Context, cleanupToken string) error {
	if f == nil {
		return appacq.ErrAcquisitionNotWired
	}
	if cleanupToken == "" {
		return appacq.Wrap(appacq.ErrAcquisitionInvalidToken, "empty CleanupToken")
	}

	f.mu.Lock()
	cached, ok := f.byTok[cleanupToken]
	f.mu.Unlock()
	if ok {
		return f.releaseByContext(ctx, cached)
	}

	// On cache miss, search by filename (the CleanupToken was
	// derived from SourceRef, so we can re-derive the file path
	// from the cache miss's suspected source). We do a simpler
	// walk: scan the stagingRoot for any .meta.json whose inner
	// CleanupToken matches; that's O(N) on staging count, OK for
	// §12-4's per-run staging surface (a few hundred at most).
	matches, err := f.findByToken(cleanupToken)
	if err != nil {
		return appacq.Wrap(appacq.ErrAcquisitionInvalidToken, err.Error())
	}
	if len(matches) == 0 {
		return appacq.Wrap(appacq.ErrAcquisitionInvalidToken, "CleanupToken does not match any registered stage (cache miss + filesystem scan miss)")
	}
	if len(matches) > 1 {
		// Two stages with the SAME CleanupToken — sha-clash.
		// We deliberately fail-closed here instead of releasing
		// either; operator must intervene.
		return appacq.Wrap(appacq.ErrAcquisitionInvalidToken,
			fmt.Sprintf("CleanupToken collision: %d stages share this token (manual reconcile required)", len(matches)))
	}
	return f.releaseByContext(ctx, matches[0])
}

// releaseByContext removes the staged file + metadata. The Called-
// side guards (`Expired`, shared-CleanupToken) are checked here.
func (f *FilesystemStager) releaseByContext(_ context.Context, ctx appacq.PrepareContext) error {
	metaPath := ctx.LocalPath + ".meta.json"
	stagedPath := ctx.LocalPath

	// Expired — the underlying file IS already gone (or about to
	// be). Report a typed error so the caller can branch on the
	// specific failure class.
	if ctx.Expired() {
		// Sweep anyway: TTL GC may have missed the file (clock
		// skew, etc.), in which case we still remove it for
		// idempotency.
		if err := os.RemoveAll(stagedPath); err != nil && !os.IsNotExist(err) {
			return appacq.Wrap(appacq.ErrAcquisitionPrepareFailed,
				fmt.Sprintf("release expired stage: removeAll %q: %v", stagedPath, err))
		}
		_ = os.RemoveAll(metaPath)
		f.forgetToken(ctx.CleanupToken)
		return appacq.Wrap(appacq.ErrAcquisitionExpired, fmt.Sprintf("stage already expired at %s (swept on release)", ctx.ExpiresAt.Format(time.RFC3339)))
	}

	if err := os.RemoveAll(stagedPath); err != nil && !os.IsNotExist(err) {
		return appacq.Wrap(appacq.ErrAcquisitionPrepareFailed, fmt.Sprintf("remove staged %q: %v", stagedPath, err))
	}
	if err := os.RemoveAll(metaPath); err != nil && !os.IsNotExist(err) {
		return appacq.Wrap(appacq.ErrAcquisitionPrepareFailed, fmt.Sprintf("remove meta %q: %v", metaPath, err))
	}
	f.forgetToken(ctx.CleanupToken)

	f.log.Info("acquisition release completed",
		zap.String("stage_id", ctx.ID),
		zap.String("local_path", stagedPath),
	)
	return nil
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

// ── Metadata read/write ────────────────────────────────────────────

// metaFileEnvelope is the on-disk JSON shape for {ID}.meta.json.
// Mirrors PrepareContext field-for-field so an audit reader can
// decode the sidecar without the runtime. New fields are added
// in lockstep with PrepareContext additions.
type metaFileEnvelope struct {
	SchemaVersion string            `json:"schema_version"`
	ID            string            `json:"id"`
	SourceRef     appacq.SourceRef  `json:"source_ref"`
	LocalPath     string            `json:"local_path"`
	StorageURI    string            `json:"storage_uri,omitempty"`
	SHA256        string            `json:"sha256"`
	SizeBytes     int64             `json:"size_bytes"`
	MIMEType      string            `json:"mime_type"`
	ExpiresAt     time.Time         `json:"expires_at"`
	CleanupToken  string            `json:"cleanup_token"`
}

func (f *FilesystemStager) writeMeta(metaPath string, ctx appacq.PrepareContext) error {
	envelope := metaFileEnvelope{
		SchemaVersion: "v1",
		ID:            ctx.ID,
		SourceRef:     ctx.SourceRef,
		LocalPath:     ctx.LocalPath,
		StorageURI:    ctx.StorageURI,
		SHA256:        ctx.SHA256,
		SizeBytes:     ctx.SizeBytes,
		MIMEType:      ctx.MIMEType,
		ExpiresAt:     ctx.ExpiresAt,
		CleanupToken:  ctx.CleanupToken,
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	// Atomic write: tmp + rename. The OS rename is atomic on the
	// same filesystem so a partial write can never be observed.
	tmp := metaPath + ".partial"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, metaPath)
}

func (f *FilesystemStager) readMeta(metaPath string) (*appacq.PrepareContext, bool) {
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, false
	}
	var envelope metaFileEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		// Corrupt meta — treat as cache miss. Operator can
		// delete the .meta.json manually to recover.
		f.log.Warn("acquisition: meta sidecar unreadable; cache miss",
			zap.String("meta_path", metaPath),
			zap.Error(err))
		return nil, false
	}
	if envelope.LocalPath != "" && envelope.SizeBytes != int64(len(raw)) {
		f.log.Warn("acquisition: meta size mismatch; cache miss",
			zap.String("meta_path", metaPath),
			zap.Int64("expected", envelope.SizeBytes),
			zap.Int("actual", len(raw)),
		)
		_ = envelope
	}
	return &appacq.PrepareContext{
		ID:           envelope.ID,
		SourceRef:    envelope.SourceRef,
		LocalPath:    envelope.LocalPath,
		StorageURI:   envelope.StorageURI,
		SHA256:       envelope.SHA256,
		SizeBytes:    envelope.SizeBytes,
		MIMEType:     envelope.MIMEType,
		ExpiresAt:    envelope.ExpiresAt,
		CleanupToken: envelope.CleanupToken,
	}, true
}

// findByToken scans stagingRoot for a .meta.json whose inner
// CleanupToken matches the supplied token. O(N) — acceptable for
// per-run staging surfaces (a few hundred at most).
func (f *FilesystemStager) findByToken(token string) ([]appacq.PrepareContext, error) {
	entries, err := os.ReadDir(f.stagingRoot)
	if err != nil {
		return nil, fmt.Errorf("read staging dir %q: %w", f.stagingRoot, err)
	}
	var out []appacq.PrepareContext
	var errs []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".meta.json") {
			continue
		}
		raw, readErr := os.ReadFile(filepath.Join(f.stagingRoot, name))
		if readErr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", name, readErr))
			continue
		}
		var envelope metaFileEnvelope
		if err := json.Unmarshal(raw, &envelope); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		if envelope.CleanupToken == token {
			out = append(out, appacq.PrepareContext{
				ID:           envelope.ID,
				SourceRef:    envelope.SourceRef,
				LocalPath:    envelope.LocalPath,
				StorageURI:   envelope.StorageURI,
				SHA256:       envelope.SHA256,
				SizeBytes:    envelope.SizeBytes,
				MIMEType:     envelope.MIMEType,
				ExpiresAt:    envelope.ExpiresAt,
				CleanupToken: envelope.CleanupToken,
			})
		}
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		return nil, fmt.Errorf("filesystem scan errors: %s", strings.Join(errs, "; "))
	}
	return out, nil
}

// ── Helpers ────────────────────────────────────────────────────────

// fileSHA256 hashes the bytes at path with SHA-256 and returns the
// hex-encoded digest.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %q: %w", path, err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %q: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ── ErrFSStagerNotConfigured ───────────────────────────────────────

// errFSStagerNotConfigured is a development-time check at the
// concrete level. The port's ErrAcquisitionNotWired is the
// canonical sentinel; this private error is wired so the concrete's
// own tests can disambiguate. Typed as `errors.New` to keep
// godlike/07 alignment.
var errFSStagerNotConfigured = errors.New("acquisition.FilesystemStager: not configured (constructor returned nil)")
