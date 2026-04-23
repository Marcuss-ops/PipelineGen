package artlistdb

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// AddSearchResults adds clips found by an Artlist search.
func (db *ArtlistDB) AddSearchResults(term string, clips []ArtlistClip) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	key := strings.ToLower(term)
	existing, ok := db.data.Searches[key]
	if !ok {
		existing = SearchResult{
			Term:              term,
			Clips:             []ArtlistClip{},
			SearchedAt:        time.Now().Format(time.RFC3339),
			DownloadedClipIDs: []string{},
		}
	}

	existingIDs := make(map[string]bool)
	for _, c := range existing.Clips {
		existingIDs[c.ID] = true
	}

	added := 0
	for _, clip := range clips {
		if !existingIDs[clip.ID] {
			clip.AddedAt = time.Now().Format(time.RFC3339)
			existing.Clips = append(existing.Clips, clip)
			existingIDs[clip.ID] = true
			added++
		}
	}

	db.data.Searches[key] = existing
	db.data.TotalClips = 0
	for _, sr := range db.data.Searches {
		db.data.TotalClips += len(sr.Clips)
	}

	logger.Info("ArtlistDB: search results saved",
		zap.String("term", term),
		zap.Int("total_clips", len(existing.Clips)),
		zap.Int("new_clips", added),
	)

	return nil
}

// MarkClipDownloaded marks a clip as downloaded and uploaded to Drive.
func (db *ArtlistDB) MarkClipDownloaded(clipID string, term string, driveFileID, driveURL, downloadPath string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	key := strings.ToLower(term)
	result, ok := db.data.Searches[key]
	if !ok {
		return fmt.Errorf("search term not found: %s", term)
	}

	found := false
	for i := range result.Clips {
		if result.Clips[i].ID == clipID {
			c := &result.Clips[i]
			c.Downloaded = true
			c.DriveFileID = driveFileID
			c.DriveURL = driveURL
			c.DownloadPath = downloadPath
			name := strings.TrimSpace(c.Name)
			if name == "" {
				name = c.VideoID
			}
			folder := strings.TrimSpace(c.Folder)
			if folder == "" {
				folder = fmt.Sprintf("Stock/Artlist/%s", term)
			}
			c.LocalPathDrive = fmt.Sprintf("%s/%s", folder, name)
			c.DownloadedAt = time.Now().Format(time.RFC3339)
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("clip not found in search results: %s", clipID)
	}

	result.DownloadedClipIDs = append(result.DownloadedClipIDs, clipID)
	if strings.TrimSpace(result.DriveFolderID) == "" {
		result.DriveFolderID = "Stock/Artlist/" + term
	}

	db.data.Searches[key] = result

	logger.Info("ArtlistDB: clip marked as downloaded",
		zap.String("term", term),
		zap.String("clip_id", clipID),
		zap.String("drive_id", driveFileID),
	)

	return nil
}

// SetDriveFolder sets the Drive folder ID for a search term.
func (db *ArtlistDB) SetDriveFolder(term string, folderID string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	key := strings.ToLower(term)
	result, ok := db.data.Searches[key]
	if !ok {
		return fmt.Errorf("search term not found: %s", term)
	}

	result.DriveFolderID = folderID
	db.data.Searches[key] = result

	return nil
}

// MarkClipUsedInVideo adds a video name to the clip's UsedInVideos list.
func (db *ArtlistDB) MarkClipUsedInVideo(clipID string, videoName string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	for termKey, result := range db.data.Searches {
		for i := range result.Clips {
			if result.Clips[i].ID == clipID {
				alreadyTracked := false
				for _, vid := range result.Clips[i].UsedInVideos {
					if vid == videoName {
						alreadyTracked = true
						break
					}
				}
				if !alreadyTracked {
					result.Clips[i].UsedInVideos = append(result.Clips[i].UsedInVideos, videoName)
				}
				db.data.Searches[termKey] = result
				return nil
			}
		}
	}

	return fmt.Errorf("clip not found: %s", clipID)
}

// DeduplicateDownloadedByVisualHash marks duplicate downloaded clips as not downloaded.
func (db *ArtlistDB) DeduplicateDownloadedByVisualHash() (DedupStats, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	type ref struct {
		term string
		idx  int
	}
	stats := DedupStats{
		DriveIDsToDelete: make([]string, 0),
	}
	seenHash := make(map[string]ref)
	deleteSet := make(map[string]bool)

	terms := make([]string, 0, len(db.data.Searches))
	for term := range db.data.Searches {
		terms = append(terms, term)
	}
	sort.Strings(terms)

	for _, term := range terms {
		result := db.data.Searches[term]
		changed := false

		for i := range result.Clips {
			c := &result.Clips[i]
			if !c.Downloaded || strings.TrimSpace(c.DriveFileID) == "" {
				continue
			}
			hash := strings.TrimSpace(c.VisualHash)
			if hash == "" {
				continue
			}
			if _, ok := seenHash[hash]; !ok {
				seenHash[hash] = ref{term: term, idx: i}
				stats.CanonicalKept++
				continue
			} else {
				if strings.TrimSpace(c.DriveFileID) != "" {
					deleteSet[c.DriveFileID] = true
				}
				c.Downloaded = false
				c.DriveFileID = ""
				c.DriveURL = ""
				c.DownloadPath = ""
				c.LocalPathDrive = ""
				c.DownloadedAt = ""
				stats.DuplicateMarked++
				changed = true
			}
		}

		if changed {
			newDownloaded := make([]string, 0, len(result.Clips))
			for _, c := range result.Clips {
				if c.Downloaded {
					newDownloaded = append(newDownloaded, c.ID)
				}
			}
			result.DownloadedClipIDs = newDownloaded
			db.data.Searches[term] = result
		}
	}

	if len(deleteSet) > 0 {
		for id := range deleteSet {
			stats.DriveIDsToDelete = append(stats.DriveIDsToDelete, id)
		}
		sort.Strings(stats.DriveIDsToDelete)
	}
	db.data.LastUpdated = time.Now().Format(time.RFC3339)
	return stats, nil
}

// ClearDeletedDriveFiles clears downloaded metadata for removed Drive files.
func (db *ArtlistDB) ClearDeletedDriveFiles(driveIDs []string) (int, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	if len(driveIDs) == 0 {
		return 0, nil
	}
	rm := make(map[string]bool, len(driveIDs))
	for _, id := range driveIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			rm[id] = true
		}
	}
	if len(rm) == 0 {
		return 0, nil
	}

	cleared := 0
	for term, result := range db.data.Searches {
		changed := false
		for i := range result.Clips {
			c := &result.Clips[i]
			if !c.Downloaded {
				continue
			}
			if rm[c.DriveFileID] {
				c.Downloaded = false
				c.DriveFileID = ""
				c.DriveURL = ""
				c.DownloadPath = ""
				c.LocalPathDrive = ""
				c.DownloadedAt = ""
				cleared++
				changed = true
			}
		}
		if changed {
			newDownloaded := make([]string, 0, len(result.Clips))
			for _, c := range result.Clips {
				if c.Downloaded {
					newDownloaded = append(newDownloaded, c.ID)
				}
			}
			result.DownloadedClipIDs = newDownloaded
			db.data.Searches[term] = result
		}
	}
	if cleared > 0 {
		db.data.LastUpdated = time.Now().Format(time.RFC3339)
	}
	return cleared, nil
}
