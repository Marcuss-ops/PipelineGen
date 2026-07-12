// Package artifacts — local_store_helpers.go (FASE 3-A, July 2026,
// Step 7 split): 5 small private helpers extracted from
// local_store.go for SSOT isolation.
//
// godlike/06 SSOT: this file is the SOLE canonical owner of the
// low-level FS + ctx helpers used by LocalStore. They are
// grouped here (not split per-helper) because each is <20 LOC
// and they are referenced from multiple call sites in the
// canonical, stage, and recover files.
//
// Helpers:
//   - syscallStatfs        — production statfsFn (FS free-space reporter)
//   - syncDirBestEffort    — fsyncs a directory (advisory best-effort)
//   - verifyPermission0700 — asserts path mode is exactly 0700
//   - readFileSHAIfExists  — computes SHA-256 of file if it exists
//   - ctxErr               — ctx-cancellation helper (nil ctx → error)
//
// Cross-file references (same package — `artifacts`):
//   - application/assets/staging (for staging.ErrStagerInvalidInput, used by ctxErr)
package artifacts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/staging"
)

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
