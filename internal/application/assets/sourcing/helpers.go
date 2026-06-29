// Package sourcing — helper functions extracted from service.go.
//
// Per AGENTS.md Pattern 5 (June 2026): one concept per file. This file holds
// the standalone helper functions used by the sourcing service.
//
// P0-1 / commit 1 (June 2026): the YouTube-specific URL parsing, query
// builder, description builder, folder-name normaliser and indexing-status
// helpers moved to internal/application/assets/sourcing/youtube/helpers.go
// (the YouTubeRegistrar sub-package). Only the LocalImporter-scoped helper
// ScanLocalMp4 remains here until P0-1 / commit 4 lifts it into
// internal/application/assets/sourcing/localimport/helpers.go.
package sourcing

import (
	"os"
	"path/filepath"
	"strings"
)

// ScanLocalMp4 scans a local directory for .mp4 files.
// This is provided as a convenience function for FileScannerPort implementors.
// Will move to internal/application/assets/sourcing/localimport/helpers.go
// in P0-1 / commit 4.
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
