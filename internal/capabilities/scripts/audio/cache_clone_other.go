//go:build !linux

package audio

import "errors"

// cloneFile is unavailable outside Linux; the caller falls back to the
// streaming copy that hashes in the same pass.
func CloneFile(string, string) error {
	return errors.New("copy-on-write reflink is not supported on this platform")
}
