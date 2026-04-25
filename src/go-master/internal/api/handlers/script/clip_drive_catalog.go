package script

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// loadClipDriveCatalog loads the clip drive catalog from JSON files
func loadClipDriveCatalog(dataDir string) ([]clipDriveRecord, error) {
	searchRoots := []string{}
	if trimmed := strings.TrimSpace(dataDir); trimmed != "" {
		searchRoots = append(searchRoots, trimmed)
	}
	if wd, err := os.Getwd(); err == nil {
		searchRoots = append(searchRoots, wd)
	}
	if exe, err := os.Executable(); err == nil {
		searchRoots = append(searchRoots, filepath.Dir(exe))
	}

	path, err := findClipIndexPath(searchRoots)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var index clipDriveIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, err
	}
	return index.Clips, nil
}

// findClipIndexPath searches for clip_index.json in the given root directories
func findClipIndexPath(searchRoots []string) (string, error) {
	seen := make(map[string]struct{})
	for _, root := range searchRoots {
		root = filepath.Clean(strings.TrimSpace(root))
		if root == "" {
			continue
		}
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}

		dir := root
		for {
			candidates := []string{
				filepath.Join(dir, "clip_index.json"),
				filepath.Join(dir, "data", "clip_index.json"),
			}
			for _, candidate := range candidates {
				if _, err := os.Stat(candidate); err == nil {
					return candidate, nil
				}
			}

			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return "", os.ErrNotExist
}