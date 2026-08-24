// Package artifacts — local_store_stage.go (FASE 3-A, July 2026,
// Step 7 split): Stage method (the 10-step staging flow) extracted
// from local_store.go for SSOT isolation.
//
// godlike/06 SSOT: this file is the SOLE canonical owner of the
// LocalStore.Stage method (the staging.Stager.Stage port
// implementation). The 10-step flow:
//
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
//  10. Build StagedReceipt.
//
// Cross-file references (same package — `artifacts`):
//   - LocalStore struct (in local_store.go canonical)
//   - partialDirName const (in local_store.go canonical)
//   - ctxErr + readFileSHAIfExists + syncDirBestEffort (in local_store_helpers.go)
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

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/staging"
)

// Stage implements staging.Stager.Stage. See file header for the
// 10-step flow overview.
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
