package drive

import (
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/driveurl"
)

// The pure URL helpers are OWNED by internal/kernel/driveurl so a capability
// can spell a Drive URL without importing this adapter. These wrappers keep
// the adapter's historical public surface stable.

// FileIDFromLink extracts a Google Drive file or folder ID from a URL.
func FileIDFromLink(link string) string {
	return driveurl.FileIDFromLink(link)
}

// FileURLFromID builds a canonical Drive file URL for an ID.
func FileURLFromID(id string) string {
	return driveurl.FileURLFromID(id)
}

// FolderURLFromID builds a canonical Drive folder URL for an ID.
func FolderURLFromID(id string) string {
	return driveurl.FolderURLFromID(id)
}

// BuildNameQuery builds a Drive API search query for a file/folder name.

// NormalizeDriveFolderLink returns a canonical folder link if folderID is provided.
