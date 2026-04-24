package clip

import (
	"strings"
	"unicode"
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
	return idx.SearchFoldersWithGroup(query, "")
}

func (idx *Indexer) SearchFoldersWithGroup(query string, group string) []IndexedFolder {
	group = strings.TrimSpace(group)
	if group == "" {
		return idx.searchFoldersFiltered(query, nil)
	}
	return idx.searchFoldersFiltered(query, func(folder IndexedFolder) bool {
		return strings.EqualFold(strings.TrimSpace(folder.Group), group)
	})
}

func (idx *Indexer) SearchFoldersInNamespace(query string, namespace string) []IndexedFolder {
	ns := strings.ToLower(strings.TrimSpace(namespace))
	if ns != "" && !strings.HasSuffix(ns, "/") {
		ns += "/"
	}
	return idx.searchFoldersFiltered(query, func(folder IndexedFolder) bool {
		pathLower := strings.ToLower(strings.TrimSpace(folder.Path))
		if ns == "" {
			return true
		}
		return strings.HasPrefix(pathLower, ns)
	})
}

func (idx *Indexer) searchFoldersFiltered(query string, include func(IndexedFolder) bool) []IndexedFolder {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if idx.index == nil {
		return []IndexedFolder{}
	}

	type scoredFolder struct {
		folder IndexedFolder
		score  int
		depth  int
	}
	queryLower := strings.ToLower(query)
	querySlug := normalizeFolderQuery(query)
	results := make([]scoredFolder, 0, len(idx.index.Folders))

	for _, folder := range idx.index.Folders {
		if include != nil && !include(folder) {
			continue
		}
		nameLower := strings.ToLower(folder.Name)
		pathLower := strings.ToLower(folder.Path)
		nameSlug := normalizeFolderQuery(folder.Name)
		pathSlug := normalizeFolderQuery(folder.Path)
		score := 0
		switch {
		case nameSlug == querySlug:
			score = 100
		case nameLower == queryLower:
			score = 95
		case strings.HasSuffix(pathSlug, "/"+querySlug):
			score = 92
		case strings.Contains(pathSlug, querySlug):
			score = 80
		case strings.Contains(pathLower, queryLower):
			score = 60
		}
		if score <= 0 {
			continue
		}
		depth := strings.Count(folder.Path, "/") + 1
		results = append(results, scoredFolder{folder: folder, score: score, depth: depth})
	}

	for i := 0; i < len(results)-1; i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].score > results[i].score || (results[j].score == results[i].score && results[j].depth < results[i].depth) {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	out := make([]IndexedFolder, 0, len(results))
	for _, r := range results {
		out = append(out, r.folder)
	}
	return out
}

func normalizeFolderQuery(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
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
