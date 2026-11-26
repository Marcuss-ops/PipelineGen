// Package driveurl owns the pure Google Drive URL helpers shared by the
// capability layer and the platform adapter.
//
// These helpers were the last reason assets/{assetop,assettree} imported
// internal/platform/drive: a three-line string builder is not infrastructure,
// and making a capability depend on the Drive adapter to spell a URL kept the
// adapter in its compile graph and its test plan. The helpers have no I/O, no
// config and no SDK dependency, so they live in the kernel and platform/drive
// re-exports them for its existing callers.
package driveurl

import urlutil "github.com/Marcuss-ops/PipelineGen/pkg/urlutil"

// FileIDFromLink extracts a Google Drive file or folder ID from a URL.
func FileIDFromLink(link string) string {
	id, _ := urlutil.FileIDFromDriveLink(link)
	return id
}

// FileURLFromID builds a canonical Drive file URL for an ID.
func FileURLFromID(id string) string {
	return "https://drive.google.com/file/d/" + id
}

// FolderURLFromID builds a canonical Drive folder URL for an ID.
func FolderURLFromID(id string) string {
	return "https://drive.google.com/drive/folders/" + id
}
