package drive

import (
	"fmt"
	"strings"
)

// MD5FromMetadata extracts MD5 checksum from a JSON metadata string.
func MD5FromMetadata(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	// Simple string search for common keys
	for _, key := range []string{"drive_md5_checksum", "md5_checksum", "file_hash"} {
		searchStr := fmt.Sprintf(`"%s":"`, key)
		if idx := strings.Index(raw, searchStr); idx >= 0 {
			start := idx + len(searchStr)
			end := strings.Index(raw[start:], `"`)
			if end >= 0 {
				return raw[start : start+end]
			}
		}
	}
	return ""
}
