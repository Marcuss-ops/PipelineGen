// Package artlistdb stores ALL Artlist clips found via search — both downloaded and just indexed.
package artlistdb

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// ArtlistDB is a local JSON database of Artlist search results.
type ArtlistDB struct {
	path string
	data *ArtlistData
	mu   sync.RWMutex
}

// ArtlistData holds all clips organized by search term.
type ArtlistData struct {
	// Searches maps search_term → list of clips found for that term
	Searches map[string]SearchResult `json:"searches"`
	// TotalClips counts all clips across all searches
	TotalClips int `json:"total_clips"`
	// LastUpdated tracks when the DB was last modified
	LastUpdated string `json:"last_updated"`
}

// SearchResult holds clips found for a specific search term.
type SearchResult struct {
	Term       string        `json:"term"`
	Clips      []ArtlistClip `json:"clips"`
	SearchedAt string        `json:"searched_at"`
	// DownloadedClips tracks which clips have been downloaded and uploaded to Drive
	DownloadedClipIDs []string `json:"downloaded_clip_ids"`
	// DriveFolderID is the Google Drive folder for this term (e.g., Stock/Artlist/Boxing)
	DriveFolderID string `json:"drive_folder_id"`
}

// ArtlistClip represents a single Artlist clip from search.
type ArtlistClip struct {
	// ID is unique identifier from Artlist DB (e.g., "artlist_vid123_456")
	ID string `json:"id"`
	// VideoID is the original Artlist video ID
	VideoID string `json:"video_id"`
	// Title/Name from Artlist
	Title string `json:"title"`
	// FileID is Google Drive file ID
	FileID string `json:"file_id,omitempty"`
	// Name (filename)
	Name string `json:"name,omitempty"`
	// Term (category)
	Term string `json:"term,omitempty"`
	// Folder path
	Folder string `json:"folder,omitempty"`
	// FolderID
	FolderID string `json:"folder_id,omitempty"`
	// OriginalURL is the Artlist download URL (never changes, used for dedup)
	OriginalURL string `json:"original_url"`
	// URL is the current download URL (may change if proxied)
	URL string `json:"url"`
	// DriveFileID is the Google Drive file ID after upload
	DriveFileID string `json:"drive_file_id,omitempty"`
	// DriveURL is the Google Drive view URL
	DriveURL string `json:"drive_url,omitempty"`
	// LocalPathDrive is the full Google Drive path (e.g., "Stock/Artlist/Boxing/clip.mp4")
	LocalPathDrive string `json:"local_path_drive,omitempty"`
	// DownloadPath is the local file path after download
	DownloadPath string `json:"download_path,omitempty"`
	// Duration in seconds
	Duration int `json:"duration"`
	// Width/Height resolution
	Width  int `json:"width"`
	Height int `json:"height"`
	// Category from Artlist
	Category string `json:"category"`
	// Tags are searchable keywords (e.g., ["boxing", "fight", "gym", "sport"])
	Tags []string `json:"tags"`
	// Embedding is a placeholder for future semantic search (optional, stores vector path or hash)
	Embedding string `json:"embedding,omitempty"`
	// Downloaded is true when the clip has been downloaded and uploaded to Drive
	Downloaded bool `json:"downloaded"`
	// AddedAt tracks when this clip was added to the DB
	AddedAt string `json:"added_at"`
	// DownloadedAt tracks when the clip was actually downloaded
	DownloadedAt string `json:"downloaded_at,omitempty"`
	// UsedInVideos tracks which video projects used this clip
	UsedInVideos []string `json:"used_in_videos,omitempty"`
}

// Open opens or creates the Artlist local DB.
func Open(path string) (*ArtlistDB, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create DB directory: %w", err)
	}

	db := &ArtlistDB{
		path: path,
		data: &ArtlistData{
			Searches:    make(map[string]SearchResult),
			TotalClips:  0,
			LastUpdated: time.Now().Format(time.RFC3339),
		},
	}

	// Load existing data
	if _, err := os.Stat(path); err == nil {
		if err := db.load(); err != nil {
			logger.Warn("Failed to load ArtlistDB, starting fresh", zap.Error(err))
		} else {
			logger.Info("ArtlistDB loaded",
				zap.Int("searches", len(db.data.Searches)),
				zap.Int("total_clips", db.data.TotalClips),
			)
		}
	}

	return db, nil
}

// load reads the DB from disk.
func (db *ArtlistDB) load() error {
	data, err := os.ReadFile(db.path)
	if err != nil {
		return fmt.Errorf("failed to read ArtlistDB: %w", err)
	}

	if err := json.Unmarshal(data, &db.data); err != nil {
		return fmt.Errorf("failed to parse ArtlistDB: %w", err)
	}

	// Ensure Searches map is initialized
	if db.data.Searches == nil {
		db.data.Searches = make(map[string]SearchResult)
	}

	return nil
}

// Save writes the DB to disk.
func (db *ArtlistDB) Save() error {
	db.mu.Lock()
	defer db.mu.Unlock()

	db.data.LastUpdated = time.Now().Format(time.RFC3339)

	data, err := json.MarshalIndent(db.data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal ArtlistDB: %w", err)
	}

	return os.WriteFile(db.path, data, 0644)
}

// GetClipsForTerm returns all clips found for a search term.
func (db *ArtlistDB) GetClipsForTerm(term string) ([]ArtlistClip, bool) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	result, ok := db.data.Searches[strings.ToLower(term)]
	if !ok {
		return nil, false
	}

	return result.Clips, true
}

// GetDownloadedClipsForTerm returns only clips that have been downloaded and uploaded to Drive.
func (db *ArtlistDB) GetDownloadedClipsForTerm(term string) ([]ArtlistClip, bool) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	result, ok := db.data.Searches[strings.ToLower(term)]
	if !ok {
		return nil, false
	}

	var downloaded []ArtlistClip
	for _, clip := range result.Clips {
		if clip.Downloaded && clip.DriveFileID != "" {
			downloaded = append(downloaded, clip)
		}
	}

	return downloaded, len(downloaded) > 0
}

// HasSearchedTerm returns true if we've already searched for this term.
func (db *ArtlistDB) HasSearchedTerm(term string) bool {
	db.mu.RLock()
	defer db.mu.RUnlock()

	_, ok := db.data.Searches[strings.ToLower(term)]
	return ok
}

// AddSearchResults adds clips found by an Artlist search.
func (db *ArtlistDB) AddSearchResults(term string, clips []ArtlistClip) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	key := strings.ToLower(term)

	// Merge with existing results (avoid duplicates)
	existing, ok := db.data.Searches[key]
	if !ok {
		existing = SearchResult{
			Term:              term,
			Clips:             []ArtlistClip{},
			SearchedAt:        time.Now().Format(time.RFC3339),
			DownloadedClipIDs: []string{},
		}
	}

	// Add new clips that don't already exist
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

	// Find and update the clip
	found := false
	for i := range result.Clips {
		if result.Clips[i].ID == clipID {
			result.Clips[i].Downloaded = true
			result.Clips[i].DriveFileID = driveFileID
			result.Clips[i].DriveURL = driveURL
			result.Clips[i].DownloadPath = downloadPath
			result.Clips[i].LocalPathDrive = fmt.Sprintf("Stock/Artlist/%s/%s", term, result.Clips[i].VideoID)
			result.Clips[i].DownloadedAt = time.Now().Format(time.RFC3339)
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("clip not found in search results: %s", clipID)
	}

	// Track downloaded clip IDs
	result.DownloadedClipIDs = append(result.DownloadedClipIDs, clipID)
	result.DriveFolderID = "Stock/Artlist/" + term

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

// DBStats holds typed database statistics.
type DBStats struct {
	TotalSearches  int    `json:"total_searches"`
	TotalClips     int    `json:"total_clips"`
	TotalDownloaded int   `json:"total_downloaded"`
	LastUpdated    string `json:"last_updated"`
}

// GetStats returns DB statistics.
func (db *ArtlistDB) GetStats() DBStats {
	db.mu.RLock()
	defer db.mu.RUnlock()

	totalDownloaded := 0
	for _, sr := range db.data.Searches {
		totalDownloaded += len(sr.DownloadedClipIDs)
	}

	return DBStats{
		TotalSearches:  len(db.data.Searches),
		TotalClips:     db.data.TotalClips,
		TotalDownloaded: totalDownloaded,
		LastUpdated:    db.data.LastUpdated,
	}
}

// GetAllTerms returns all search terms in the DB.
func (db *ArtlistDB) GetAllTerms() []string {
	db.mu.RLock()
	defer db.mu.RUnlock()

	terms := make([]string, 0, len(db.data.Searches))
	for term := range db.data.Searches {
		terms = append(terms, term)
	}

	return terms
}

// FindClipByOriginalURL searches for a clip by its original URL across all terms.
// Returns the clip and the term it belongs to, or false if not found.
func (db *ArtlistDB) FindClipByOriginalURL(url string) (ArtlistClip, string, bool) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	for term, result := range db.data.Searches {
		for _, clip := range result.Clips {
			if clip.OriginalURL == url || clip.URL == url {
				return clip, term, true
			}
		}
	}

	return ArtlistClip{}, "", false
}

// FindDownloadedClipsWithSimilarTags searches for downloaded clips that have similar tags.
// Returns clips that share at least minTagMatch tags with the input tags.
func (db *ArtlistDB) FindDownloadedClipsWithSimilarTags(tags []string, minTagMatch int) ([]ArtlistClip, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	if len(tags) == 0 {
		return nil, nil
	}

	tagSet := make(map[string]bool)
	for _, t := range tags {
		tagSet[strings.ToLower(t)] = true
	}

	var results []ArtlistClip

	for _, result := range db.data.Searches {
		for _, clip := range result.Clips {
			if !clip.Downloaded || clip.DriveFileID == "" {
				continue
			}

			// Count matching tags
			matchCount := 0
			for _, clipTag := range clip.Tags {
				if tagSet[strings.ToLower(clipTag)] {
					matchCount++
				}
			}

			if matchCount >= minTagMatch {
				results = append(results, clip)
			}
		}
	}

	return results, nil
}

// IsClipAlreadyDownloaded checks if a clip with the same ID or URL is already downloaded.
func (db *ArtlistDB) IsClipAlreadyDownloaded(clipID, url string) (ArtlistClip, bool) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	for _, result := range db.data.Searches {
		for _, clip := range result.Clips {
			if !clip.Downloaded {
				continue
			}
			if clip.ID == clipID || clip.URL == url || clip.OriginalURL == url {
				return clip, true
			}
		}
	}

	return ArtlistClip{}, false
}

// MarkClipUsedInVideo adds a video name to the clip's UsedInVideos list.
func (db *ArtlistDB) MarkClipUsedInVideo(clipID string, videoName string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	for termKey, result := range db.data.Searches {
		for i := range result.Clips {
			if result.Clips[i].ID == clipID {
				// Check if already tracked
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

// GetDownloadedClipsByTag returns all downloaded clips that have a specific tag.
func (db *ArtlistDB) GetDownloadedClipsByTag(tag string) ([]ArtlistClip, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	tagLower := strings.ToLower(tag)
	var results []ArtlistClip

	for _, result := range db.data.Searches {
		for _, clip := range result.Clips {
			if !clip.Downloaded {
				continue
			}
			for _, clipTag := range clip.Tags {
				if strings.ToLower(clipTag) == tagLower {
					results = append(results, clip)
					break
				}
			}
		}
	}

	return results, nil
}

// GetUniqueDownloadedClipsForTerm returns unique downloaded clips for a term,
// excluding those already used in a specific video (for variety).
func (db *ArtlistDB) GetUniqueDownloadedClipsForTerm(term string, excludeUsedInVideo string) ([]ArtlistClip, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	key := strings.ToLower(term)
	result, ok := db.data.Searches[key]
	if !ok {
		return nil, nil
	}

	var clips []ArtlistClip
	for _, clip := range result.Clips {
		if !clip.Downloaded || clip.DriveFileID == "" {
			continue
		}

		// Skip if already used in the specified video
		if excludeUsedInVideo != "" {
			used := false
			for _, vid := range clip.UsedInVideos {
				if vid == excludeUsedInVideo {
					used = true
					break
				}
			}
			if used {
				continue
			}
		}

		clips = append(clips, clip)
	}

	return clips, nil
}
