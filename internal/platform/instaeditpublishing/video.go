package instaeditpublishing

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

type videoFile struct {
	Path        string
	Filename    string
	ContentType string
	Size        int64
	SHA256      string
}

func inspectVideo(filePath string) (videoFile, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return videoFile{}, fmt.Errorf("open video: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return videoFile{}, fmt.Errorf("stat video: %w", err)
	}
	if !info.Mode().IsRegular() {
		return videoFile{}, fmt.Errorf("video path is not a regular file")
	}
	sha256Hex, err := digest.SHA256Reader(file)
	if err != nil {
		return videoFile{}, fmt.Errorf("hash video: %w", err)
	}
	filename := filepath.Base(filePath)
	contentType, err := videoContentType(filename)
	if err != nil {
		return videoFile{}, err
	}
	return videoFile{
		Path:        filePath,
		Filename:    filename,
		ContentType: contentType,
		Size:        info.Size(),
		SHA256:      sha256Hex,
	}, nil
}

func videoContentType(filename string) (string, error) {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".mp4":
		return "video/mp4", nil
	case ".mov":
		return "video/quicktime", nil
	case ".m4v":
		return "video/x-m4v", nil
	default:
		return "", fmt.Errorf("unsupported video extension %q (expected .mp4, .mov or .m4v)", filepath.Ext(filename))
	}
}

func hasHeader(headers map[string]string, wanted string) bool {
	for key := range headers {
		if strings.EqualFold(key, wanted) {
			return true
		}
	}
	return false
}

func targetIDFor(targets []postTarget, platformAccountID int64) int64 {
	for _, target := range targets {
		if target.PlatformAccountID == platformAccountID && target.ID != 0 {
			return target.ID
		}
	}
	if len(targets) == 1 {
		return targets[0].ID
	}
	return 0
}

func cleanTags(tags []string) []string {
	seen := make(map[string]struct{}, len(tags))
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		key := strings.ToLower(tag)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, tag)
	}
	return out
}
