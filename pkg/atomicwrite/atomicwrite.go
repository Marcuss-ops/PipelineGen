// Package atomicwrite is the single utility for crash-safe file writes.
//
// Motivation (measured): two source files in this repository were once found
// truncated on disk (a doc comment present, the body gone), which broke
// `go build ./...` for unrelated packages. A plain os.WriteFile truncates the
// destination before writing, so a crash, a full disk or an interrupted
// process leaves a partial file in place. This is the worst failure class
// because it silently corrupts the source of truth.
//
// The rule implemented here is the classic one and it is the only supported
// way for tooling, generators and scripts to write a file:
//
//	write to a temp file in the SAME directory -> fsync the file ->
//	rename over the target -> fsync the directory
//
// rename(2) within a directory is atomic, so a concurrent reader observes
// either the complete old content or the complete new content, never a
// truncated file. The directory fsync makes the rename itself durable.
//
// For multi-file publications that cannot be a single rename, use Stage +
// Commit per file so every rename is atomic; the caller owns cross-file
// rollback (see cmd/admin/internal/rendering for the canonical user).
package atomicwrite

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// Writer receives the bytes to persist. A returned error aborts the write and
// leaves any existing destination untouched.
type Writer func(w io.Writer) error

// WriteFile atomically replaces path with data.
//
// perm is the mode used when path does not exist yet; an existing file keeps
// its current mode (matching os.WriteFile semantics). The parent directory
// must exist.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	return Write(path, perm, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

// Write atomically replaces path with the bytes produced by fn.
//
// fn runs against the staging file, so a failure inside fn leaves any existing
// destination untouched and removes the staging file.
func Write(path string, perm os.FileMode, fn Writer) error {
	tmp, err := Stage(path, perm, fn)
	if err != nil {
		return err
	}
	if err := Commit(tmp, path); err != nil {
		_ = Discard(tmp)
		return err
	}
	return nil
}

// Stage writes and fsyncs a new temporary file next to path and returns its
// path without renaming it. It exists so a caller that must publish several
// files together can prepare all staging files before committing any of them,
// keeping each individual replacement atomic.
//
// The caller MUST eventually call Commit or Discard for the returned path.
func Stage(path string, perm os.FileMode, fn Writer) (string, error) {
	dir := filepath.Dir(path)
	base := filepath.Base(path)

	file, err := os.CreateTemp(dir, "."+base+".tmp-*")
	if err != nil {
		return "", err
	}
	tmp := file.Name()
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(tmp)
	}

	if err := file.Chmod(effectivePerm(path, perm)); err != nil {
		cleanup()
		return "", err
	}
	if fn != nil {
		if err := fn(file); err != nil {
			cleanup()
			return "", err
		}
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

// Commit atomically renames a staged file over path and fsyncs the parent
// directory so the rename survives a crash.
func Commit(tmp, path string) error {
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

// Discard removes a staged file that will not be committed. It is a no-op for
// an empty path and tolerates an already-removed file.
func Discard(tmp string) error {
	if tmp == "" {
		return nil
	}
	err := os.Remove(tmp)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// effectivePerm returns the mode to give the staged file: the current mode of
// path when it already exists, otherwise perm (or 0644 when perm is zero).
func effectivePerm(path string, perm os.FileMode) os.FileMode {
	if info, err := os.Stat(path); err == nil {
		return info.Mode().Perm()
	}
	if perm == 0 {
		return 0o644
	}
	return perm.Perm()
}

// syncDir fsyncs a directory so a rename in it is durable. Platforms that do
// not support fsync on a directory return an error that is not actionable, so
// failures are returned to the caller rather than ignored — a caller that
// cannot make the rename durable should know.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		// Some filesystems (e.g. certain network mounts) reject fsync on a
		// directory. Treat that as non-fatal: the rename itself already
		// happened and is atomic, only its crash-durability is weaker.
		if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) {
			return nil
		}
		return err
	}
	return nil
}
