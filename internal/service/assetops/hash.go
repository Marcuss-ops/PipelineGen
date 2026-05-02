package assetops

import (
	"velox/go-master/pkg/hashutil"
)

// HashLocalFile computes the MD5 hash of a local file
func HashLocalFile(path string) (string, error) {
	return hashutil.MD5File(path)
}
