package idutil

import (
	"fmt"

	"velox/go-master/pkg/hashutil"
)

// StableSlugID generates a stable ID with a slug prefix and truncated MD5 hash of parts.
func StableSlugID(prefix string, parts ...string) string {
	shortHash := hashutil.ShortMD5(parts, 12)
	return fmt.Sprintf("%s_%s", prefix, shortHash)
}
