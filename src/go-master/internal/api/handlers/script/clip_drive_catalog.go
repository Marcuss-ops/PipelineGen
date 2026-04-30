package script

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
	"velox/go-master/internal/repository/clips"
)

// loadClipsFromDB loads the clips from the database
func loadClipsFromDB(ctx context.Context, repo *clips.Repository, source string) ([]clipDriveRecord, error) {
	if repo == nil {
		return nil, nil
	}

	dbClips, err := repo.ListClips(ctx, source)
	if err != nil {
		return nil, err
	}

	zap.L().Info("loadClipsFromDB called", zap.String("source", source), zap.Int("count", len(dbClips)))

	records := make([]clipDriveRecord, 0, len(dbClips))
	for _, c := range dbClips {
		records = append(records, clipDriveRecord{
			ID:           c.ID,
			Name:         c.Name,
			Filename:     c.Filename,
			FolderID:     c.FolderID,
			FolderPath:   c.FolderPath,
			Group:        c.Group,
			MediaType:    c.MediaType,
			DriveLink:    c.DriveLink,
			DownloadLink: c.DownloadLink,
			Tags:         c.Tags,
			Source:       c.Source,
		})
	}
	return records, nil
}

// loadClipDriveCatalog loads the clip drive catalog from JSON files (legacy) or DB
func loadClipDriveCatalog(ctx context.Context, dataDir string, repo *clips.Repository) ([]clipDriveRecord, error) {
	// Try DB first (Single Source of Truth)
	if repo != nil {
		records, err := loadClipsFromDB(ctx, repo, "")
		if err == nil && len(records) > 0 {
			return records, nil
		}
	}

	// Fallback to JSON
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
