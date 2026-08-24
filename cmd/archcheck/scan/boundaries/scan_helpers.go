package boundaries

import "strings"

func hasAnyPathPrefix(relPath string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(relPath, p) {
			return true
		}
	}
	return false
}
