package drive

import (
	"velox/go-master/pkg/drive"
)

// FileIDFromLink extracts a Google Drive file ID from various URL formats.
// DEPRECATED: Use pkg/drive.FileIDFromLink instead.
func FileIDFromLink(raw string) string {
	return drive.FileIDFromLink(raw)
}
