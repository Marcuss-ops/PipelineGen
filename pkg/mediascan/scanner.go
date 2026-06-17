package mediascan

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// MediaFile represents a scanned media file
type MediaFile struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	URL     string    `json:"url,omitempty"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

// ScanDirectory scans a directory for media files
func ScanDirectory(root string, urlPrefix string) ([]MediaFile, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	var files []MediaFile
	err = filepath.WalkDir(absRoot, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d == nil || d.IsDir() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		rel, err := filepath.Rel(absRoot, path)
		if err != nil {
			return nil
		}

		var url string
		if urlPrefix != "" {
			url = strings.TrimRight(urlPrefix, "/") + "/" + filepath.ToSlash(rel)
		}

		files = append(files, MediaFile{
			Name:    d.Name(),
			Path:    path,
			URL:     url,
			Size:    info.Size(),
			ModTime: info.ModTime().UTC(),
		})
		return nil
	})

	if err != nil {
		return nil, err
	}

	// Sort by modification time descending (newest first)
	sort.Slice(files, func(i, j int) bool {
		return files[i].ModTime.After(files[j].ModTime)
	})

	return files, nil
}
