// Package clipcache provides idempotent caching for clip searches and usage tracking.
package clipcache

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ClipCache stores segment hashes and clip usage to avoid redundant Artlist searches.
type ClipCache struct {
	path string
	data *ClipCacheData
	mu   sync.RWMutex
}

// ClipCacheData holds all cached data.
type ClipCacheData struct {
	// Segments maps segment_hash → cached search result
	Segments map[string]CachedSegment `json:"segments"`
	// ClipUsages tracks which clips were used for which segments
	ClipUsages []ClipUsageRecord `json:"clip_usages"`
	// LastUpdated tracks when the cache was last modified
	LastUpdated string `json:"last_updated"`
}

// CachedSegment holds cached search results for a segment.
type CachedSegment struct {
	SegmentHash string      `json:"segment_hash"`
	SegmentText string      `json:"segment_text"`
	QueriesUsed []string    `json:"queries_used"`
	Clips       []ClipRecord `json:"clips"`
	CachedAt    string      `json:"cached_at"`
	HitCount    int         `json:"hit_count"`
}

// ClipRecord represents a cached clip result.
type ClipRecord struct {
	// SearchQuery is the original search query that found this clip
	SearchQuery string `json:"search_query"`
	// VideoID is the clip identifier from Artlist
	VideoID string `json:"video_id"`
	// ClipID is our internal ID
	ClipID    string   `json:"clip_id"`
	Title     string   `json:"title"`
	URL       string   `json:"url"`
	DriveURL  string   `json:"drive_url,omitempty"`
	DriveFileID string `json:"drive_file_id,omitempty"`
	DrivePath string   `json:"drive_path,omitempty"`
	Tags      []string `json:"tags"`
	Duration  int      `json:"duration"`
	Score     float64  `json:"score"`
	Downloaded bool   `json:"downloaded"`
	// ViewCount tracks how many times this clip was used
	ViewCount int `json:"view_count"`
	// DownloadedAt is when the clip was downloaded
	DownloadedAt int64 `json:"downloaded_at"`
	// LastUsedAt is when the clip was last used
	LastUsedAt int64 `json:"last_used_at"`
	// UseCount tracks total uses
	UseCount int `json:"use_count"`
}

// ClipUsageRecord tracks clip usage across videos.
type ClipUsageRecord struct {
	VideoID     string `json:"video_id"`
	SegmentHash string `json:"segment_hash"`
	ClipID      string `json:"clip_id"`
	Rank        int    `json:"rank"`
	DrivePath   string `json:"drive_path"`
	UsedAt      string `json:"used_at"`
}

// Open opens or creates the clip cache.
func Open(path string) (*ClipCache, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	cc := &ClipCache{
		path: path,
		data: &ClipCacheData{
			Segments:    make(map[string]CachedSegment),
			ClipUsages:  []ClipUsageRecord{},
			LastUpdated: time.Now().Format(time.RFC3339),
		},
	}

	// Load existing data
	if _, err := os.Stat(path); err == nil {
		if err := cc.load(); err != nil {
			// Start fresh if load fails
			cc.data.Segments = make(map[string]CachedSegment)
		}
	}

	return cc, nil
}

// load reads the cache from disk.
func (cc *ClipCache) load() error {
	data, err := os.ReadFile(cc.path)
	if err != nil {
		return fmt.Errorf("failed to read cache: %w", err)
	}

	if err := json.Unmarshal(data, &cc.data); err != nil {
		return fmt.Errorf("failed to parse cache: %w", err)
	}

	if cc.data.Segments == nil {
		cc.data.Segments = make(map[string]CachedSegment)
	}

	return nil
}

// Save writes the cache to disk.
func (cc *ClipCache) Save() error {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	cc.data.LastUpdated = time.Now().Format(time.RFC3339)

	data, err := json.MarshalIndent(cc.data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal cache: %w", err)
	}

	return os.WriteFile(cc.path, data, 0644)
}

// ComputeSegmentHash computes a SHA1 hash of segment text for dedup.
func ComputeSegmentHash(text string) string {
	h := sha1.Sum([]byte(text))
	return hex.EncodeToString(h[:])
}

// GetCachedSegments returns cached clips for a segment hash.
// Returns clips and true if found, or nil and false if not cached.
func (cc *ClipCache) GetCachedSegments(segmentHash string) ([]ClipRecord, bool) {
	cc.mu.RLock()
	segment, ok := cc.data.Segments[segmentHash]
	if !ok || len(segment.Clips) == 0 {
		cc.mu.RUnlock()
		return nil, false
	}
	clips := make([]ClipRecord, len(segment.Clips))
	copy(clips, segment.Clips)
	cc.mu.RUnlock()

	// Increment hit count under write lock
	cc.mu.Lock()
	segment.HitCount++
	cc.data.Segments[segmentHash] = segment
	cc.mu.Unlock()

	return clips, true
}

// CacheSegments stores search results for a segment.
func (cc *ClipCache) CacheSegments(segmentHash, segmentText string, queries []string, clips []ClipRecord) error {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	cc.data.Segments[segmentHash] = CachedSegment{
		SegmentHash: segmentHash,
		SegmentText: segmentText,
		QueriesUsed: queries,
		Clips:       clips,
		CachedAt:    time.Now().Format(time.RFC3339),
		HitCount:    1,
	}

	return nil
}

// RecordClipUsage records that a clip was used in a video.
func (cc *ClipCache) RecordClipUsage(videoID, segmentHash, clipID string, rank int, drivePath string) error {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	// Check if already recorded
	for _, usage := range cc.data.ClipUsages {
		if usage.VideoID == videoID && usage.SegmentHash == segmentHash && usage.ClipID == clipID {
			return nil // Already recorded
		}
	}

	cc.data.ClipUsages = append(cc.data.ClipUsages, ClipUsageRecord{
		VideoID:     videoID,
		SegmentHash: segmentHash,
		ClipID:      clipID,
		Rank:        rank,
		DrivePath:   drivePath,
		UsedAt:      time.Now().Format(time.RFC3339),
	})

	return nil
}

// GetReusedClips returns clips that were previously used for similar segments.
// This allows reusing clips across videos when segments match.
func (cc *ClipCache) GetReusedClips(segmentHashes []string) []ClipRecord {
	cc.mu.RLock()
	defer cc.mu.RUnlock()

	clipMap := make(map[string]ClipRecord)

	for _, hash := range segmentHashes {
		segment, ok := cc.data.Segments[hash]
		if !ok {
			continue
		}

		for _, clip := range segment.Clips {
			if clip.Downloaded {
				clipMap[clip.ClipID] = clip
			}
		}
	}

	var clips []ClipRecord
	for _, clip := range clipMap {
		clips = append(clips, clip)
	}

	return clips
}

// GetClipUsageHistory returns usage history for a specific clip.
func (cc *ClipCache) GetClipUsageHistory(clipID string) []ClipUsageRecord {
	cc.mu.RLock()
	defer cc.mu.RUnlock()

	var usages []ClipUsageRecord
	for _, usage := range cc.data.ClipUsages {
		if usage.ClipID == clipID {
			usages = append(usages, usage)
		}
	}

	return usages
}

// ClipCacheStats holds typed cache statistics.
type ClipCacheStats struct {
	TotalSegments int     `json:"total_segments"`
	TotalUsages   int     `json:"total_usages"`
	TotalHits     int     `json:"total_hits"`
	CacheHitRate  float64 `json:"cache_hit_rate"`
	LastUpdated   string  `json:"last_updated"`
}

// GetStats returns cache statistics.
func (cc *ClipCache) GetStats() ClipCacheStats {
	cc.mu.RLock()
	defer cc.mu.RUnlock()

	totalHits := 0
	for _, segment := range cc.data.Segments {
		totalHits += segment.HitCount
	}

	return ClipCacheStats{
		TotalSegments: len(cc.data.Segments),
		TotalUsages:   len(cc.data.ClipUsages),
		TotalHits:     totalHits,
		CacheHitRate:  float64(totalHits) / maxFloat(1, float64(len(cc.data.Segments))),
		LastUpdated:   cc.data.LastUpdated,
	}
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// CachedClip represents a clip stored in cache (compatibility type).
type CachedClip = ClipRecord

// Search returns a cached clip matching the query, or nil if not found.
func (cc *ClipCache) Search(query string) *CachedClip {
	cc.mu.RLock()
	defer cc.mu.RUnlock()

	queryLower := strings.ToLower(query)

	for _, segment := range cc.data.Segments {
		for _, clip := range segment.Clips {
			if clip.Downloaded {
				// Check if clip has matching tag, title, or search query
				if strings.Contains(strings.ToLower(clip.Title), queryLower) ||
				   strings.Contains(strings.ToLower(clip.SearchQuery), queryLower) {
					for _, tag := range clip.Tags {
						if strings.Contains(strings.ToLower(tag), queryLower) {
							return &clip
						}
					}
				}
			}
		}
	}

	return nil
}

// Store saves a clip to the cache.
func (cc *ClipCache) Store(clip *CachedClip) error {
	// Find existing segment or create one
	segmentHash := ComputeSegmentHash(clip.Title)

	cc.mu.Lock()
	defer cc.mu.Unlock()

	segment, ok := cc.data.Segments[segmentHash]
	if !ok {
		segment = CachedSegment{
			SegmentHash: segmentHash,
			SegmentText: clip.Title,
			CachedAt:    time.Now().Format(time.RFC3339),
			HitCount:    1,
		}
	}

	// Add clip if not already present
	for _, existing := range segment.Clips {
		if existing.ClipID == clip.ClipID {
			return nil
		}
	}

	segment.Clips = append(segment.Clips, *clip)
	cc.data.Segments[segmentHash] = segment

	return nil
}

// Cleanup removes old unused clips from the cache and returns count of removed items.
func (cc *ClipCache) Cleanup() int {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	removed := 0
	cutoff := time.Now().AddDate(0, 0, -90)

	for hash, segment := range cc.data.Segments {
		if cachedAt, err := time.Parse(time.RFC3339, segment.CachedAt); err == nil {
			if cachedAt.Before(cutoff) && segment.HitCount == 0 {
				delete(cc.data.Segments, hash)
				removed++
			}
		}
	}

	return removed
}

// UpdateUseCount increments the usage count for a clip.
func (cc *ClipCache) UpdateUseCount(clipID string) {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	for hash, segment := range cc.data.Segments {
		for i := range segment.Clips {
			if segment.Clips[i].ClipID == clipID {
				segment.Clips[i].UseCount++
				segment.Clips[i].LastUsedAt = time.Now().Unix()
				cc.data.Segments[hash] = segment
				return
			}
		}
	}
}
