package filesystem

import (
	"io"
	"os"
	"path/filepath"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/job/workspace"
)

// OS implements workspace.FileSystem against the real local filesystem.
// It is the canonical production adapter; the kernel's FileSystem port
// deliberately carries neutral types (uint32 permission bits, FileEntry)
// so the os driver never leaks into the kernel contract.
type OS struct{}

// NewOS constructs the OS filesystem adapter.
func NewOS() *OS { return &OS{} }

// compile-time assertion: OS satisfies the kernel FileSystem port.
var _ workspace.FileSystem = (*OS)(nil)

func (OS) Abs(path string) (string, error) {
	return filepath.Abs(path)
}

func (OS) MkdirAll(path string, perm uint32) error {
	return os.MkdirAll(path, os.FileMode(perm))
}

func (OS) OpenFile(path string, perm uint32) (io.WriteCloser, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(perm))
}

func (OS) Remove(path string) error {
	return os.Remove(path)
}

func (OS) RemoveAll(path string) error {
	return os.RemoveAll(path)
}

func (OS) Lstat(path string) (workspace.FileEntry, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return workspace.FileEntry{}, nil
		}
		return workspace.FileEntry{}, err
	}
	return workspace.FileEntry{
		Exists:    true,
		IsSymlink: info.Mode()&os.ModeSymlink != 0,
	}, nil
}
