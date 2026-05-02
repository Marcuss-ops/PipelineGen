package drive

import "strings"

// escapeQuery escapes single quotes for use in Drive API queries.
func escapeQuery(s string) string {
	return strings.ReplaceAll(s, "'", "\\'")
}
