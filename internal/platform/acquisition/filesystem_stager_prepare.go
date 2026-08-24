package acquisition

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"

	appacq "github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
)

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
	releasePrepareLock := f.acquirePrepareLock(stageID)
	defer releasePrepareLock()

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
	// Remove the closure's stale FINAL output from a prior Prepare that
	// failed after fetch but before the atomic rename. This is NOT the
	// resumable surface: yt-dlp's own `.part` artifacts live under the
	// downloader's output template (e.g. {partialPath}.mp4.part) and are
	// deliberately left in place so a job re-claimed after a graceful
	// server restart resumes via the downloader's `--continue` flag
	// instead of re-fetching the source from 0% (PR-STOCK-RESUME).
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
