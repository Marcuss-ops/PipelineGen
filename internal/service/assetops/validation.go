package assetops

import (
	"os"
)

// ValidateLocalFile checks if a local file exists and is not a directory
func ValidateLocalFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}
