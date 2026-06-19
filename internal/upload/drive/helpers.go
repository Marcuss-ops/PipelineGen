package drive

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

// BuildNameQuery builds a Drive API search query for a file/folder name.
func BuildNameQuery(parentID, name, mimeType string) string {
	q := "'" + name + "' in name and trashed = false"
	if parentID != "" {
		q += " and '" + parentID + "' in parents"
	}
	if mimeType != "" {
		q += " and mimeType = '" + mimeType + "'"
	}
	return q
}

// NormalizeDriveFolderLink returns a canonical folder link if folderID is provided.
func NormalizeDriveFolderLink(link, folderID string) string {
	if folderID != "" {
		return FolderURLFromID(folderID)
	}
	return link
}
