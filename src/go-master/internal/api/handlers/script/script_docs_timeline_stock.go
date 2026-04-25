package script

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// buildStockCatalogContext builds a context string from stock catalog.
func buildStockCatalogContext(dataDir string, limit int) string {
	folders, err := loadStockFolderCatalog(dataDir)
	if err != nil || len(folders) == 0 || limit <= 0 {
		return ""
	}

	var b strings.Builder
	seen := make(map[string]struct{}, len(folders))
	count := 0
	for _, folder := range folders {
		path := strings.TrimSpace(folder.StockPath())
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		count++
		b.WriteString(fmt.Sprintf("%d. %s\n", count, path))
		if count >= limit {
			break
		}
	}
	return strings.TrimSpace(b.String())
}

// buildDiscoveryContext builds a discovery context with grouped stock folders.
func buildDiscoveryContext(dataDir, topic string, maxGroups, maxPerGroup int) string {
	folders, err := loadStockFolderCatalog(dataDir)
	if err != nil || len(folders) == 0 || maxGroups <= 0 || maxPerGroup <= 0 {
		return ""
	}

	grouped := make(map[string][]string)
	order := make([]string, 0, len(folders))
	for _, folder := range folders {
		path := strings.TrimSpace(folder.StockPath())
		if path == "" {
			continue
		}
		group := folderGroupFromPath(path)
		if group == "" {
			group = "Ungrouped"
		}
		if _, ok := grouped[group]; !ok {
			order = append(order, group)
		}
		grouped[group] = append(grouped[group], path)
	}

	preferred := normalizeMatchText(topic)
	sort.SliceStable(order, func(i, j int) bool {
		gi := strings.ToLower(order[i])
		gj := strings.ToLower(order[j])
		mi := strings.Contains(preferred, normalizeMatchText(gi))
		mj := strings.Contains(preferred, normalizeMatchText(gj))
		if mi != mj {
			return mi
		}
		return len(grouped[order[i]]) > len(grouped[order[j]])
	})

	if len(order) > maxGroups {
		order = order[:maxGroups]
	}

	var b strings.Builder
	for _, group := range order {
		paths := grouped[group]
		if len(paths) > maxPerGroup {
			paths = paths[:maxPerGroup]
		}
		b.WriteString(fmt.Sprintf("[%s]\n", group))
		for _, path := range paths {
			b.WriteString(fmt.Sprintf("  - %s\n", path))
		}
	}

	return strings.TrimSpace(b.String())
}

// folderGroupFromPath extracts the group from a stock path.
func folderGroupFromPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	parts := strings.Split(filepath.ToSlash(path), "/")
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}

// matchStockForSegment matches stock clips for a timeline segment.
func matchStockForSegment(dataDir, topic string, seg TimelineSegment) []scoredMatch {
	clips, err := loadStockCatalog(dataDir)
	if err != nil {
		return nil
	}

	terms := collectTopicTerms(topic)
	terms = append(terms, seg.Keywords...)
	terms = append(terms, seg.Entities...)
	terms = append(terms, collectBodyTerms(seg)...)
	terms = uniqueStrings(terms)

	matches := buildMatches(clips, terms, seg)
	if len(matches) == 0 {
		return nil
	}

	scored := scoreMatches(matches, terms, seg)
	if len(scored) == 0 {
		return nil
	}

	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	limit := 8
	if len(scored) > limit {
		scored = scored[:limit]
	}

	return scored
}

// collectBodyTerms collects terms from segment body content.
func collectBodyTerms(seg TimelineSegment) []string {
	return uniqueStrings([]string{
		seg.OpeningSentence,
		seg.ClosingSentence,
		strings.Join(seg.Keywords, " "),
		strings.Join(seg.Entities, " "),
	})
}

// enrichTimelineSegments enriches timeline segments with stock matches.
func enrichTimelineSegments(plan *TimelinePlan, dataDir string, req ScriptDocsRequest) {
	if plan == nil || len(plan.Segments) == 0 {
		return
	}

	for i := range plan.Segments {
		matches := matchStockForSegment(dataDir, req.Topic, plan.Segments[i])
		plan.Segments[i].StockMatches = cloneScoredMatches(matches)
	}
}

// cloneScoredMatches creates a copy of scored matches.
func cloneScoredMatches(matches []scoredMatch) []scoredMatch {
	if matches == nil {
		return nil
	}
	cloned := make([]scoredMatch, len(matches))
	copy(cloned, matches)
	return cloned
}
