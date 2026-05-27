package drive

import (
	"fmt"
	"strings"
)

// BuildNameQuery builds a query to find a file/folder by name in a specific folder.
func BuildNameQuery(folderID, name, mimeType string) string {
	escapedName := strings.ReplaceAll(name, "'", "\\'")
	escapedFolderID := strings.ReplaceAll(folderID, "'", "\\'")

	query := fmt.Sprintf("name = '%s' and '%s' in parents and trashed=false", escapedName, escapedFolderID)

	if mimeType != "" {
		query += fmt.Sprintf(" and mimeType = '%s'", mimeType)
	}

	return query
}
