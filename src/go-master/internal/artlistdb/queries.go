package artlistdb

import (
	"strings"
)

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
		if clip.Downloaded && clip.DriveFileID != "" && !isPreviewArtlistClipDB(clip) {
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

// GetStats returns DB statistics.
func (db *ArtlistDB) GetStats() DBStats {
	db.mu.RLock()
	defer db.mu.RUnlock()

	totalDownloaded := 0
	for _, sr := range db.data.Searches {
		totalDownloaded += len(sr.DownloadedClipIDs)
	}

	return DBStats{
		TotalSearches:   len(db.data.Searches),
		TotalClips:      db.data.TotalClips,
		TotalDownloaded: totalDownloaded,
		LastUpdated:     db.data.LastUpdated,
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
			if !clip.Downloaded || clip.DriveFileID == "" || isPreviewArtlistClipDB(clip) {
				continue
			}

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
			if !clip.Downloaded || isPreviewArtlistClipDB(clip) {
				continue
			}
			if clip.ID == clipID || clip.URL == url || clip.OriginalURL == url {
				return clip, true
			}
		}
	}

	return ArtlistClip{}, false
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

// GetUniqueDownloadedClipsForTerm returns unique downloaded clips for a term.
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
		if !clip.Downloaded || clip.DriveFileID == "" || isPreviewArtlistClipDB(clip) {
			continue
		}

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

func isPreviewArtlistClipDB(c ArtlistClip) bool {
	lc := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	return strings.Contains(lc(c.Name), "preview") ||
		strings.Contains(lc(c.Title), "preview") ||
		strings.Contains(lc(c.URL), "preview") ||
		strings.Contains(lc(c.OriginalURL), "preview")
}
