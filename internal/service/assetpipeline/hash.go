package assetpipeline

import (
	"velox/go-master/pkg/hashutil"
)

func HashFile(path string) (string, error) {
	return hashutil.MD5File(path)
}

func ContentHashFile(path string) (string, error) {
	return hashutil.SHA256File(path)
}
