// Package artifacts — local_store_recover.go (FASE 3-A, July 2026,
// Step 7 split): RecoverOrphans + workspaceTotalBytes extracted
// from local_store.go for SSOT isolation.
//
// godlike/06 SSOT: this file is the SOLE canonical owner of the
// recovery flow (orphan .tmp unlink) + the workspace walker that
// populates the per-stage quota probe. Both methods are
// tightly coupled to the .partial/ subdirectory convention
// (partialDirName const, defined in local_store.go canonical).
//
// Cross-file references (same package — `artifacts`):
//   - LocalStore struct (in local_store.go canonical)
//   - partialDirName const (in local_store.go canonical)
//   - ctxErr (in local_store_helpers.go)
package artifacts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/staging"
)

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
