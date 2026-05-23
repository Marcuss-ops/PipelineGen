package drive

import (
	"net/url"
	"regexp"
	"strings"
)

var driveFilePathRE = regexp.MustCompile(`/file/d/([^/]+)`)
var driveFolderPathRE = regexp.MustCompile(`/folders/([^/?]+)`)

// FileIDFromLink extracts a file or folder ID from a Google Drive link.
// Supports formats:
//   - https://drive.google.com/file/d/FILE_ID/view
//   - https://drive.google.com/uc?id=FILE_ID
//   - https://drive.google.com/drive/folders/FOLDER_ID
func FileIDFromLink(link string) string {
	link = strings.TrimSpace(link)
	if link == "" {
		return ""
	}

	if strings.HasPrefix(link, "http") {
		if u, err := url.Parse(link); err == nil {
			if id := strings.TrimSpace(u.Query().Get("id")); id != "" {
				return id
			}

			if match := driveFilePathRE.FindStringSubmatch(u.Path); len(match) == 2 {
				return match[1]
			}

			if match := driveFolderPathRE.FindStringSubmatch(u.Path); len(match) == 2 {
				return match[1]
			}
		}
	}

	return ""
}

// NormalizeDriveFolderLink returns a Drive folder link if link or folderID is set.
func NormalizeDriveFolderLink(driveLink, folderID string) string {
	driveLink = strings.TrimSpace(driveLink)
	folderID = strings.TrimSpace(folderID)
	if isDriveFolderLink(driveLink) {
		return driveLink
	}
	if folderID != "" {
		return "https://drive.google.com/drive/folders/" + folderID
	}
	return ""
}

func isDriveFolderLink(link string) bool {
	link = strings.ToLower(strings.TrimSpace(link))
	return strings.Contains(link, "drive.google.com/drive/folders/")
}
