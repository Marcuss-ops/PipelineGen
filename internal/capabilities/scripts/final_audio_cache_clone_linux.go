//go:build linux

package scriptgeneration

import (
	"os"

	"golang.org/x/sys/unix"
)

// cloneFile asks the filesystem for a copy-on-write clone (reflink). It
// returns an error on filesystems that do not support it, and the caller falls
// back to a streaming copy. The clone is an independent file: mutating either
// side never changes the other, which is the property the audio cache needs
// from a staged master.
func cloneFile(dst, src string) error {
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	dest, err := os.OpenFile(dst, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer func() { _ = dest.Close() }()
	return unix.IoctlFileClone(int(dest.Fd()), int(source.Fd()))
}
