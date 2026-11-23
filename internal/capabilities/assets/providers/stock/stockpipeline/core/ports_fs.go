package core

import (
	"io"
	"io/fs"
)

// LocalFSPort is the Pattern 0 typed port for local filesystem I/O.
//
// godlike/07 PR-REFACTOR-P0-IO-BINDER (July 2026): the application
// layer MUST NOT import "os" or call os.* directly. Every file
// read/write/stat call goes through this port; the concrete
// implementation lives in internal/platform/filesystem and
// is injected via the SourceCacheDeps.LocalFS dependency at
// composition time.
//
// SSOT: this interface lives only in this package (the only
// consumer across the stock pipeline). Concrete implementations
// (filesystem.LocalAdapter) implement it structurally.
type LocalFSPort interface {
	// Stat returns the FileInfo for the named file.
	Stat(name string) (fs.FileInfo, error)
	// Open opens the named file for reading.
	Open(name string) (io.ReadCloser, error)
	// Create creates or truncates the named file for writing.
	Create(name string) (io.WriteCloser, error)
	// MkdirTemp creates a new temporary directory and returns its path.
	MkdirTemp(dir, pattern string) (string, error)
	// Remove removes the named file or (empty) directory.
	Remove(name string) error
	// RemoveAll removes path and any children it contains.
	RemoveAll(path string) error
	// MkdirAll creates a directory along with any necessary parents.
	MkdirAll(path string, perm fs.FileMode) error
	// CreateTemp creates a new temporary file, returning its path
	// and a WriteCloser for writing. The caller must close the
	// returned WriteCloser before using the path for hashing.
	CreateTemp(dir, pattern string) (string, io.WriteCloser, error)
	// TempDir returns the default directory to use for temporary files.
	TempDir() string
}
