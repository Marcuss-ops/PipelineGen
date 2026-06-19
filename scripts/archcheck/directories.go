package main

import (
	"os"
	"path/filepath"
	"strings"
)

var allowedInternalRoots = map[string]bool{
	"api":            true,
	"app":            true,
	"application":    true,
	"domain":         true,
	"infrastructure": true,
}

func FindDirectories(root string) ([]string, []string, error) {
	var internalDirs []string
	var invalidRoots []string

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return nil
		}

		name := info.Name()
		if name == ".git" || name == "vendor" || name == "node_modules" || name == "archcheck" {
			return filepath.SkipDir
		}

		relPath, _ := filepath.Rel(root, path)
		relPath = filepath.ToSlash(relPath)

		if relPath == "." || relPath == "" {
			return nil
		}

		// We care about directories inside internal/
		if strings.HasPrefix(relPath, "internal/") {
			parts := strings.Split(relPath, "/")
			if len(parts) >= 2 {
				// parts[1] is the root under internal
				if !allowedInternalRoots[parts[1]] {
					invalidRoots = append(invalidRoots, relPath)
				}
			}
			internalDirs = append(internalDirs, relPath)
		}

		return nil
	})

	return internalDirs, invalidRoots, err
}
