// Package stockpipeline — fake_local_fs_test.go (PR-REFACTOR-P0-IO-BINDER, July 2026).
//
// fakeLocalFS is the canonical test fake for LocalFSPort. Each method
// is a configurable function field; tests that need a specific behaviour
// set the corresponding field. Tests that just need interface satisfaction
// use &fakeLocalFS{} — every unconfigured method returns a descriptive
// "not configured" error so silent nil-panic and silent real-I/O bugs
// are impossible (godlike/07 fail-closed).
//
// Tests that need real filesystem I/O should use filesystem.NewLocal()
// directly (source_cache_test.go already does this with testFS).
package assets

import (
	"fmt"
	"io"
	"io/fs"
	"os"
)

// fakeLocalFS is a configurable LocalFSPort fake for tests.
// Every method is a function field; unconfigured methods return
// a typed "not configured" error.
type fakeLocalFS struct {
	StatFn       func(name string) (fs.FileInfo, error)
	OpenFn       func(name string) (io.ReadCloser, error)
	CreateFn     func(name string) (io.WriteCloser, error)
	MkdirTempFn  func(dir, pattern string) (string, error)
	RemoveFn     func(name string) error
	RemoveAllFn  func(path string) error
	MkdirAllFn   func(path string, perm fs.FileMode) error
	CreateTempFn func(dir, pattern string) (string, io.WriteCloser, error)
	TempDirFn    func() string
}

func (f *fakeLocalFS) Stat(name string) (fs.FileInfo, error) {
	if f.StatFn != nil {
		return f.StatFn(name)
	}
	return nil, fmt.Errorf("fakeLocalFS: Stat(%q) not configured", name)
}

func (f *fakeLocalFS) Open(name string) (io.ReadCloser, error) {
	if f.OpenFn != nil {
		return f.OpenFn(name)
	}
	return nil, fmt.Errorf("fakeLocalFS: Open(%q) not configured", name)
}

func (f *fakeLocalFS) Create(name string) (io.WriteCloser, error) {
	if f.CreateFn != nil {
		return f.CreateFn(name)
	}
	return nil, fmt.Errorf("fakeLocalFS: Create(%q) not configured", name)
}

func (f *fakeLocalFS) MkdirTemp(dir, pattern string) (string, error) {
	if f.MkdirTempFn != nil {
		return f.MkdirTempFn(dir, pattern)
	}
	return "", fmt.Errorf("fakeLocalFS: MkdirTemp(%q, %q) not configured", dir, pattern)
}

func (f *fakeLocalFS) Remove(name string) error {
	if f.RemoveFn != nil {
		return f.RemoveFn(name)
	}
	return fmt.Errorf("fakeLocalFS: Remove(%q) not configured", name)
}

func (f *fakeLocalFS) RemoveAll(path string) error {
	if f.RemoveAllFn != nil {
		return f.RemoveAllFn(path)
	}
	return fmt.Errorf("fakeLocalFS: RemoveAll(%q) not configured", path)
}

func (f *fakeLocalFS) MkdirAll(path string, perm fs.FileMode) error {
	if f.MkdirAllFn != nil {
		return f.MkdirAllFn(path, perm)
	}
	return fmt.Errorf("fakeLocalFS: MkdirAll(%q, %o) not configured", path, perm)
}

func (f *fakeLocalFS) CreateTemp(dir, pattern string) (string, io.WriteCloser, error) {
	if f.CreateTempFn != nil {
		return f.CreateTempFn(dir, pattern)
	}
	return "", nil, fmt.Errorf("fakeLocalFS: CreateTemp(%q, %q) not configured", dir, pattern)
}

func (f *fakeLocalFS) TempDir() string {
	if f.TempDirFn != nil {
		return f.TempDirFn()
	}
	return "/tmp/fake-local-fs"
}

// Compile-time assertion: *fakeLocalFS satisfies LocalFSPort.
var _ LocalFSPort = (*fakeLocalFS)(nil)

// newRealishFakeLocalFS returns a *fakeLocalFS that delegates every
// LocalFSPort method to the real OS filesystem. Tests that need the
// cutter to write real files (e.g. fakeSucceedingCutter uses os.WriteFile)
// must wire this so executeCuts can create workspace directories, metadata
// steps can create temp files, and publish steps can stat output paths.
//
// The base fakeLocalFS{} (with no function fields set) remains strict
// fail-closed (godlike/07): unconfigured methods return typed errors.
// Tests that need real I/O explicitly opt in via this constructor.
func newRealishFakeLocalFS() *fakeLocalFS {
	return &fakeLocalFS{
		StatFn: func(name string) (fs.FileInfo, error) {
			return os.Stat(name)
		},
		OpenFn: func(name string) (io.ReadCloser, error) {
			return os.Open(name)
		},
		CreateFn: func(name string) (io.WriteCloser, error) {
			return os.Create(name)
		},
		MkdirTempFn: func(dir, pattern string) (string, error) {
			return os.MkdirTemp(dir, pattern)
		},
		RemoveFn: func(name string) error {
			return os.Remove(name)
		},
		RemoveAllFn: func(path string) error {
			return os.RemoveAll(path)
		},
		MkdirAllFn: func(path string, perm fs.FileMode) error {
			return os.MkdirAll(path, os.FileMode(perm))
		},
		CreateTempFn: func(dir, pattern string) (string, io.WriteCloser, error) {
			f, err := os.CreateTemp(dir, pattern)
			if err != nil {
				return "", nil, err
			}
			return f.Name(), f, nil
		},
		TempDirFn: func() string {
			return os.TempDir()
		},
	}
}
