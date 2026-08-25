package filesystem

import "strings"

// CleanFolderName normalizes a folder name for fuzzy comparison by
// lowercasing and stripping hyphens, underscores, and spaces.
func CleanFolderName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, " ", "")
	return s
}
