// Package sourcing — helper functions extracted from service.go.
//
// Per AGENTS.md Pattern 5 (June 2026): one concept per file. This file holds
// the standalone helper functions used by the sourcing service.
package sourcing

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ── YouTube URL helpers ─────────────────────────────────────────────────────

func extractVideoIDFromURL(rawURL string) string {
	// youtube.com/watch?v=ID
	for _, part := range strings.Split(rawURL, "&") {
		if strings.HasPrefix(part, "v=") || strings.Contains(part, "?v=") {
			if idx := strings.Index(part, "v="); idx != -1 {
				id := part[idx+2:]
				if len(id) > 11 {
					id = id[:11]
				}
				return id
			}
		}
	}
	// youtu.be/ID
	if idx := strings.LastIndex(rawURL, "youtu.be/"); idx != -1 {
		rest := rawURL[idx+len("youtu.be/"):]
		if end := strings.IndexAny(rest, "?&#"); end != -1 {
			rest = rest[:end]
		}
		return rest
	}
	return ""
}

func extractURLParam(rawURL, key string) float64 {
	prefixes := []string{"&" + key + "=", "?" + key + "="}
	for _, pfx := range prefixes {
		if idx := strings.Index(rawURL, pfx); idx != -1 {
			rest := rawURL[idx+len(pfx):]
			for i, c := range rest {
				if c == '&' || c == '?' || c == '#' {
					rest = rest[:i]
					break
				}
			}
			var v float64
			if _, err := fmt.Sscanf(rest, "%f", &v); err == nil {
				return v
			}
		}
	}
	return 0
}

// ── Query / description builders ────────────────────────────────────────────

func buildRelatedClipsQuery(name, category string, tags []string) string {
	var parts []string
	if cat := strings.TrimSpace(category); cat != "" {
		parts = append(parts, cat)
	}
	maxTags := 2
	for _, t := range tags {
		if maxTags <= 0 {
			break
		}
		if tt := strings.TrimSpace(t); tt != "" {
			parts = append(parts, tt)
			maxTags--
		}
	}
	if n := strings.TrimSpace(name); n != "" {
		parts = append(parts, n)
	}
	return strings.Join(parts, " ")
}

func buildDriveDescription(name, reqDesc, fetchedDesc string, tags []string, category, source, url, videoID string) string {
	var parts []string
	if name != "" {
		parts = append(parts, "Name: "+name)
	}
	if reqDesc != "" {
		parts = append(parts, "Description: "+reqDesc)
	} else if fetchedDesc != "" {
		parts = append(parts, "Description: "+fetchedDesc)
	}
	if category != "" {
		parts = append(parts, "Category: "+category)
	}
	if source != "" {
		parts = append(parts, "Source: "+source)
	}
	if len(tags) > 0 {
		parts = append(parts, "Tags: "+strings.Join(tags, ", "))
	}
	if url != "" {
		parts = append(parts, "URL: "+url)
	}
	if videoID != "" {
		parts = append(parts, "VideoID: "+videoID)
	}
	return strings.Join(parts, "\n")
}

// ── String helpers ──────────────────────────────────────────────────────────

func cleanFolderName(name string) string {
	return strings.TrimSpace(strings.ToLower(name))
}

func indexStatus(indexed bool) string {
	if indexed {
		return "enqueued"
	}
	return "not_configured"
}

// ── File scanner helper ─────────────────────────────────────────────────────

// ScanLocalMp4 scans a local directory for .mp4 files.
// This is provided as a convenience function for FileScannerPort implementors.
func ScanLocalMp4(root string, limit int) ([]LocalFileInfo, error) {
	var out []LocalFileInfo
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".mp4") {
			return nil
		}
		if limit > 0 && len(out) >= limit {
			return filepath.SkipAll
		}
		rel, _ := filepath.Rel(root, path)
		base := strings.TrimSuffix(d.Name(), filepath.Ext(d.Name()))
		dir := filepath.Dir(path)

		// Extract group name from first subdir
		parts := strings.Split(filepath.ToSlash(rel), "/")
		groupName := ""
		if len(parts) > 1 {
			groupName = parts[0]
		}

		// Look for metadata sibling
		metaPath := ""
		for _, candidate := range []string{
			filepath.Join(dir, "metadata_"+base+".json"),
			filepath.Join(dir, base+".metadata.json"),
			filepath.Join(dir, "metadata.json"),
		} {
			if _, e := os.Stat(candidate); e == nil {
				metaPath = candidate
				break
			}
		}

		// Look for transcript sibling
		transcript := ""
		for _, candidate := range []string{
			filepath.Join(dir, base+".txt"),
			filepath.Join(dir, "transcript.txt"),
		} {
			if data, e := os.ReadFile(candidate); e == nil {
				transcript = string(data)
				break
			}
		}

		fi, _ := d.Info()
		var size int64
		if fi != nil {
			size = fi.Size()
		}
		out = append(out, LocalFileInfo{
			Path:         path,
			RelPath:      filepath.ToSlash(rel),
			Name:         base,
			GroupName:    groupName,
			Size:         size,
			MetadataPath: metaPath,
			Transcript:   transcript,
		})
		return nil
	})
	return out, err
}
