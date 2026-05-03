// Package drive provides shared utilities for Google Drive operations.
//
// STATUS: ACTIVE - Shared utilities for Drive API queries.
package drive

import (
	"fmt"
	"strings"
)

// BuildQuery builds a Google Drive API query string.
// It properly escapes parameters and provides a consistent way to build queries.
// The base query is "'<folderID>' in parents and trashed=false".
// Additional conditions can be added via the conditions parameter.
//
// Example:
//
//	query := BuildQuery("root", "mimeType='application/vnd.google-apps.folder'")
//	// Returns: "'root' in parents and trashed=false and mimeType='application/vnd.google-apps.folder'"
func BuildQuery(folderID string, conditions ...string) string {
	// Escape single quotes in folderID
	escapedFolderID := strings.ReplaceAll(folderID, "'", "\\'")
	query := fmt.Sprintf("'%s' in parents and trashed=false", escapedFolderID)

	for _, cond := range conditions {
		if cond != "" {
			query += " and " + cond
		}
	}

	return query
}

// EscapeString escapes a string for use in Drive API query.
// Single quotes are escaped by doubling them (Google Drive API format).
func EscapeString(s string) string {
	return strings.ReplaceAll(s, "'", "\\'")
}

// BuildNameQuery builds a query to find a file/folder by name in a specific folder.
// This is a common pattern: "name = '<name>' and '<folderID>' in parents and trashed=false".
func BuildNameQuery(folderID, name, mimeType string) string {
	escapedName := EscapeString(name)
	escapedFolderID := strings.ReplaceAll(folderID, "'", "\\'")

	query := fmt.Sprintf("name = '%s' and '%s' in parents and trashed=false", escapedName, escapedFolderID)

	if mimeType != "" {
		query += fmt.Sprintf(" and mimeType = '%s'", mimeType)
	}

	return query
}
