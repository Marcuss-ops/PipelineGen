// Package drive — uploader_put_helpers.go: conflict-rename and idempotency helpers.
//
// 2026-07-06 (Pattern 5 split): extracted from uploader_put.go. Owns the
// file-rename helper (renameWithTimestamp), the idempotency-key appProperty
// helper (setAppProperties), and the error-message truncation helper (truncate16).
package drive

import (
	"fmt"
	"path/filepath"
	"strings"

	driveapi "google.golang.org/api/drive/v3"
)

// renameWithTimestamp inserts a UnixNano suffix into the filename
// preserving the extension. Example:
//
//	clip.mp4               → clip_1719612345123456789.mp4
//	complex.name.v2.tar.gz → complex.name.v2_1719612345123456789.tar.gz
func renameWithTimestamp(name string, ts int64) string {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	if ext == "" {
		return fmt.Sprintf("%s_%d", base, ts)
	}
	return fmt.Sprintf("%s_%d%s", base, ts, ext)
}

// setAppProperties (P0.6) sets the pipelinegen_idempotency_key
// appProperty on a Drive File metadata struct. When idemKey is empty,
// the function is a no-op (no appProperty set).
func setAppProperties(file *driveapi.File, idemKey string) {
	if idemKey != "" {
		file.AppProperties = map[string]string{
			"pipelinegen_idempotency_key": idemKey,
		}
	}
}

// truncate16 returns the first 16 characters of s, or s itself if shorter.
// Used for error-message prefixes to avoid leaking full idempotency keys.
func truncate16(s string) string {
	if len(s) > 16 {
		return s[:16]
	}
	return s
}
