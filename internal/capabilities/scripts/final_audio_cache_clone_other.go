//go:build !linux

package scriptgeneration

import "errors"

// cloneFile is unavailable outside Linux; the caller falls back to the
// streaming copy that hashes in the same pass.
func cloneFile(string, string) error {
	return errors.New("copy-on-write reflink is not supported on this platform")
}
