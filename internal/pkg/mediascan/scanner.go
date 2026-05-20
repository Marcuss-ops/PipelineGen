package mediascan

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// MediaKind represents the type of media (video, image, etc.)
type MediaKind string

const (
	KindVideo MediaKind = "video"
	KindImage MediaKind = "image"
	KindOther MediaKind = "other"
)

// MediaFile represents a scanned media file
type MediaFile struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	URL     string    `json:"url,omitempty"`
	Kind    MediaKind `json:"kind"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

var allowedExtensions = map[string]MediaKind{
	".mp4":  KindVideo,
	".mov":  KindVideo,
	".mkv":  KindVideo,
	".webm": KindVideo,
	".jpg":  KindImage,
	".jpeg": KindImage,
	".png":  KindImage,
	".webp": KindImage,
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

		ext := strings.ToLower(filepath.Ext(d.Name()))
		kind, ok := allowedExtensions[ext]
		if !ok {
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
			Kind:    kind,
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

// IsMediaFile checks if a file extension is supported
func IsMediaFile(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	_, ok := allowedExtensions[ext]
	return ok
}

// GetMediaKind returns the kind of media based on extension
func GetMediaKind(filename string) MediaKind {
	ext := strings.ToLower(filepath.Ext(filename))
	if kind, ok := allowedExtensions[ext]; ok {
		return kind
	}
	return KindOther
}
