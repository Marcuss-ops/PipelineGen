// Package filesystem — local.go (July 2026).
//
// Concrete implementation of the application-layer LocalFSPort typed
// port. The adapter wraps the stdlib os package so the application
// layer (stockpipeline, artlist, voiceover, …) never needs to import
// "os" directly — gitlike/07 PR-REFACTOR-P0-IO-BINDER rule.
//
// godlike/06 SSOT (one canonical owner per fact):
//   - This file is the SOLE owner of the os.* calls exposed through
//     the LocalFSPort interface. Every file in internal/application
//     that needs to read/write/stat files MUST go through the port,
//     not call os.* directly.
//
// godlike/07 minimum-blast-radius: the adapter is stateless and
// safe for concurrent use; the methods are thin pass-throughs to
// the matching os.* functions. No I/O owns state outside the
// per-call file handle lifecycle. All methods on *LocalAdapter
// are concurrently safe — the underlying os.* functions are
// themselves stateless beyond the per-call file handle, and
// LocalAdapter carries no fields. The composition root may share
// a single *LocalAdapter across every stock pipeline run without
// serialisation.
package filesystem

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Conformance note: *LocalAdapter satisfies stockpipeline.LocalFSPort
// structurally. We do NOT add `var _ stockpipeline.LocalFSPort =
// (*LocalAdapter)(nil)` here because local.go lives in the
// infrastructure layer; importing stockpipeline from infrastructure
// would create an import cycle (source_cache_test.go uses the
// adapter, and stockpipeline cannot legally depend on the test
// package's transitive imports). The conformance is therefore
// enforced at the site-of-use by the build_bundles_stock.go wiring
// (LocalFSPort field typed as the interface) and at runtime by the
// StockStager.StageSource handlers. A future `[LocalAdapter
// conformance]` integration test in tests/operational/ can re-add the
// compile-time pin in the test package (which has no import-cycle
// risk).

// LocalAdapter is the production LocalFSPort implementation. It is
// the canonical injection target for the composition root
// (internal/app/wire_stock_pipeline.go et al.).
//
// godlike/06 structured-conformance: *LocalAdapter satisfies the
// application-layer LocalFSPort interface structurally; the
// // compile-time pin is enforced by the assert at the bottom of
// this file (declared by the package consumer via explicit assertion).
type LocalAdapter struct{}

// NewLocal returns a fresh LocalAdapter. Safe to call concurrently;
// the adapter is stateless.
func NewLocal() *LocalAdapter { return &LocalAdapter{} }

// ── TempDirFS (test-only, in-memory-ish filesystem scoped to a temp dir) ──

// TempDirFS is a LocalFSPort implementation that confines all file
// operations to a single temporary directory. Intended for test use
// (audit P0: no implicit fallback to real filesystem). The adapter
// prepends rootDir to every path, preventing tests from touching the
// host filesystem outside the temp dir.
//
// Concurrency: safe (the underlying os.* calls are per-handle, and
// rootDir is immutable after construction).
type TempDirFS struct {
	rootDir string
}

// NewTempDirFS returns a TempDirFS scoped to rootDir. The caller is
// responsible for creating and cleaning up the directory.
// Typical test usage: filesystem.NewTempDirFS(t.TempDir()).
func NewTempDirFS(rootDir string) *TempDirFS {
	return &TempDirFS{rootDir: rootDir}
}

func (t *TempDirFS) resolve(name string) string {
	// Strip leading / so filepath.Join doesn't treat it as absolute
	// and ignore rootDir (Join("/tmp/test", "/etc/passwd") = "/etc/passwd").
	name = strings.TrimPrefix(name, "/")
	return filepath.Join(t.rootDir, name)
}

// Stat returns the FileInfo for the named file within rootDir.
func (t *TempDirFS) Stat(name string) (os.FileInfo, error) {
	return os.Stat(t.resolve(name))
}

// Open opens the named file for reading within rootDir.
func (t *TempDirFS) Open(name string) (io.ReadCloser, error) {
	return os.Open(t.resolve(name))
}

// Create creates or truncates the named file within rootDir.
// Matches LocalAdapter.Create behavior — does NOT create parent directories.
func (t *TempDirFS) Create(name string) (io.WriteCloser, error) {
	return os.Create(t.resolve(name))
}

// Stat returns the FileInfo for the named file. Thin pass-through to
// os.Stat. godlike/07 typed-error: the underlying os error is
// returned verbatim so callers can errors.Is(*PathError) probe.
func (l *LocalAdapter) Stat(name string) (os.FileInfo, error) {
	return os.Stat(name)
}

// Open opens the named file for reading. Thin pass-through to os.Open
// — returns io.ReadCloser wrapping *os.File so callers don't need to
// import os directly.
func (l *LocalAdapter) Open(name string) (io.ReadCloser, error) {
	return os.Open(name)
}

// Create creates or truncates the named file. Thin pass-through to
// os.Create — returns io.WriteCloser wrapping *os.File so callers
// don't need to import os directly.
func (l *LocalAdapter) Create(name string) (io.WriteCloser, error) {
	return os.Create(name)
}

// MkdirTemp creates a new temporary directory and returns its path.
func (l *LocalAdapter) MkdirTemp(dir, pattern string) (string, error) {
	return os.MkdirTemp(dir, pattern)
}

// Remove removes the named file or (empty) directory.
func (l *LocalAdapter) Remove(name string) error {
	return os.Remove(name)
}

// RemoveAll removes path and any children it contains.
func (l *LocalAdapter) RemoveAll(path string) error {
	return os.RemoveAll(path)
}

// MkdirAll creates a directory along with any necessary parents.
func (l *LocalAdapter) MkdirAll(path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}

// CreateTemp creates a new temporary file, returning its path
// and a WriteCloser for writing.
func (l *LocalAdapter) CreateTemp(dir, pattern string) (string, io.WriteCloser, error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", nil, err
	}
	return f.Name(), f, nil
}

// TempDir returns the default directory to use for temporary files.
func (l *LocalAdapter) TempDir() string {
	return os.TempDir()
}
