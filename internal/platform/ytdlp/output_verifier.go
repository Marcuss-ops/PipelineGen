package ytdlp

import (
	"fmt"
	"os"
)

// OutputVerifier validates yt-dlp file outputs after a successful process
// exit. Blocco 1c (July 2026) established that yt-dlp can exit zero while
// leaving an empty or missing output file; this verifier makes the stat
// check explicit so callers don't silently proceed with a dead path.
type OutputVerifier struct{}

// VerifyFile checks that path exists, is a regular file, and is non-empty.
// Returns nil on success or a descriptive error on failure.
func (v *OutputVerifier) VerifyFile(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("output file %q not found: %w", path, err)
	}
	if fi.IsDir() {
		return fmt.Errorf("output path %q is a directory, expected a regular file", path)
	}
	if fi.Size() == 0 {
		return fmt.Errorf("output file %q is empty (yt-dlp exited successfully but produced no data)", path)
	}
	return nil
}
