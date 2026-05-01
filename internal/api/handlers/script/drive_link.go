package script

import (
	"net/url"
	"strings"
)

const driveFolderPrefix = "https://drive.google.com/drive/folders/"

func normalizeDriveFolderLink(driveLink, folderID string) string {
	folderID = strings.TrimSpace(folderID)
	if link := strings.TrimSpace(driveLink); isDriveFolderLink(link) {
		return link
	}
	if link := extractDriveFolderLink(driveLink); link != "" {
		return link
	}
	if folderID != "" && !isDriveFileLink(driveLink) {
		return driveFolderPrefix + folderID
	}
	if folderID != "" {
		return driveFolderPrefix + folderID
	}
	return ""
}

func isDriveFileLink(link string) bool {
	link = strings.TrimSpace(link)
	if link == "" {
		return false
	}
	return strings.Contains(link, "drive.google.com/file/") || strings.Contains(link, "docs.google.com/document/")
}

func isDriveFolderLink(link string) bool {
	link = strings.TrimSpace(link)
	if link == "" {
		return false
	}
	return strings.Contains(link, "drive.google.com/drive/folders/") || strings.Contains(link, "drive.google.com/drive/u/")
}

func extractDriveFolderLink(link string) string {
	link = strings.TrimSpace(link)
	if link == "" {
		return ""
	}

	parsed, err := url.Parse(link)
	if err != nil {
		return ""
	}
	if parsed.Host != "drive.google.com" {
		return ""
	}

	trimmedPath := strings.Trim(parsed.Path, "/")
	parts := strings.Split(trimmedPath, "/")
	if len(parts) >= 3 && parts[0] == "drive" && parts[1] == "folders" {
		folderID := strings.TrimSpace(parts[2])
		if folderID != "" {
			return "https://drive.google.com" + parsed.Path
		}
	}
	if len(parts) >= 5 && parts[0] == "drive" && parts[1] == "u" && parts[3] == "folders" {
		folderID := strings.TrimSpace(parts[4])
		if folderID != "" {
			return "https://drive.google.com" + parsed.Path
		}
	}
	return ""
}

func extractDriveFolderID(link string) string {
	link = strings.TrimSpace(link)
	if link == "" {
		return ""
	}

	parsed, err := url.Parse(link)
	if err != nil {
		return ""
	}
	if parsed.Host != "drive.google.com" {
		return ""
	}

	trimmedPath := strings.Trim(parsed.Path, "/")
	parts := strings.Split(trimmedPath, "/")
	if len(parts) >= 3 && parts[0] == "drive" && parts[1] == "folders" {
		return strings.TrimSpace(parts[2])
	}
	if len(parts) >= 5 && parts[0] == "drive" && parts[1] == "u" && parts[3] == "folders" {
		return strings.TrimSpace(parts[4])
	}
	return ""
}
