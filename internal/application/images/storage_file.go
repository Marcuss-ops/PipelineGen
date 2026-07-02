package images

import (
	"fmt"
	"os"
	"path/filepath"
)

func persistImageBytes(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create image directory: %w", err)
	}
	if err := os.WriteFile(path, content, 0644); err != nil {
		return fmt.Errorf("write image file: %w", err)
	}
	return nil
}
