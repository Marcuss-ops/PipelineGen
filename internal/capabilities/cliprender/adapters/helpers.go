package adapters

import "strings"

// firstNonEmpty returns the first non-empty value after trimming, or "".
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
