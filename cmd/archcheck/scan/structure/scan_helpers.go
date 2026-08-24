package structure

import "strings"

const assetStateCanonical14Path = "internal/kernel/asset/asset_state_values.go"

func hasAnyPathPrefix(relPath string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(relPath, p) {
			return true
		}
	}
	return false
}
