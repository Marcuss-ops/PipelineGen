package config

import (
	"os"
	"path/filepath"
	"strings"
)

// RuntimeDataDir resolves the canonical runtime data directory.
// VELOX_DATA_DIR wins; otherwise the local ./data directory is used.
func RuntimeDataDir() string {
	if v := strings.TrimSpace(os.Getenv("VELOX_DATA_DIR")); v != "" {
		return v
	}
	return "./data"
}

// ResolveDataPath returns a path under the canonical runtime data directory.
func ResolveDataPath(filename string) string {
	return filepath.Join(RuntimeDataDir(), filename)
}
