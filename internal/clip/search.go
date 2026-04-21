package clip

import (
	"strings"
)

func (idx *Indexer) Search(query string, filters SearchFilters) []IndexedClip {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if idx.index == nil {
		return []IndexedClip{}
	}

	var results []IndexedClip
	queryLower := strings.ToLower(query)
	queryTerms := strings.Fields(queryLower)

	for _, clip := range idx.index.Clips {
		if !idx.matchesFilters(clip, filters) {
			continue
		}

		if query == "" {
			results = append(results, clip)
			continue
		}

		score := idx.calculateSearchScore(clip, query, queryTerms, queryLower)
		if score > 0 {
			results = append(results, clip)
		}
	}

	return results
}

func (idx *Indexer) SearchFolders(query string) []IndexedFolder {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if idx.index == nil {
		return []IndexedFolder{}
	}

	var results []IndexedFolder
	queryLower := strings.ToLower(query)

	for _, folder := range idx.index.Folders {
		if strings.Contains(strings.ToLower(folder.Name), queryLower) ||
			strings.Contains(strings.ToLower(folder.Path), queryLower) {
			results = append(results, folder)
		}
	}

	return results
}

func (idx *Indexer) matchesFilters(clip IndexedClip, filters SearchFilters) bool {
	if filters.Group != "" && !strings.EqualFold(clip.Group, filters.Group) {
		return false
	}

	if filters.MediaType != "" && !strings.EqualFold(clip.MediaType, filters.MediaType) {
		return false
	}

	if filters.FolderID != "" && clip.FolderID != filters.FolderID {
		return false
	}

	if filters.MinDuration > 0 && clip.Duration < filters.MinDuration {
		return false
	}

	if filters.MaxDuration > 0 && clip.Duration > filters.MaxDuration {
		return false
	}

	if filters.Resolution != "" && !strings.EqualFold(clip.Resolution, filters.Resolution) {
		return false
	}

	if len(filters.Tags) > 0 {
		hasTag := false
		for _, tag := range filters.Tags {
			if containsTag(clip.Tags, strings.ToLower(tag)) {
				hasTag = true
				break
			}
		}
		if !hasTag {
			return false
		}
	}

	return true
}

func (idx *Indexer) calculateSearchScore(clip IndexedClip, query string, queryTerms []string, queryLower string) float64 {
	var score float64

	clipNameLower := strings.ToLower(clip.Name)
	clipPathLower := strings.ToLower(clip.FolderPath)
	filenameLower := strings.ToLower(clip.Filename)

	if clipNameLower == queryLower {
		score += 100
	}

	if strings.Contains(clipNameLower, queryLower) {
		score += 50
	}

	for _, tag := range clip.Tags {
		if strings.Contains(tag, queryLower) {
			score += 40
		}
	}

	if strings.Contains(clipPathLower, queryLower) {
		score += 30
	}

	if strings.Contains(filenameLower, queryLower) {
		score += 20
	}

	for _, term := range queryTerms {
		if strings.Contains(clipNameLower, term) {
			score += 10
		}
		if containsTag(clip.Tags, term) {
			score += 8
		}
		if strings.Contains(clipPathLower, term) {
			score += 5
		}
	}

	return score
}
