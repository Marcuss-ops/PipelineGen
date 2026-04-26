package script

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"velox/go-master/internal/repository/clips"
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

// matchStockForSegment matches stock clips for a timeline segment using the clips repository.
func matchStockForSegment(repo *clips.Repository, ctx context.Context, topic string, seg TimelineSegment) []scoredMatch {
	if repo == nil {
		return nil
	}

	terms := collectTopicTerms(topic)
	terms = append(terms, seg.Keywords...)
	terms = append(terms, seg.Entities...)
	terms = append(terms, collectBodyTerms(seg)...)
	terms = uniqueStrings(terms)

	// Search clips from DB
	dbClips, err := repo.SearchStockByKeywords(ctx, terms, 20)
	if err != nil || len(dbClips) == 0 {
		return nil
	}

	matches := make([]scoredMatch, 0, len(dbClips))
	for _, clip := range dbClips {
		score := scoreText(strings.ToLower(strings.Join([]string{
			clip.Name,
			clip.Filename,
			clip.FolderPath,
			clip.Group,
			strings.Join(clip.Tags, " "),
			clip.Source,
			clip.Category,
		}, " ")), terms)

		if score == 0 {
			continue
		}

		link := clip.ExternalURL
		if link == "" {
			link = clip.DriveLink
		}

		matches = append(matches, scoredMatch{
			Title:  clip.Name,
			Score:  score,
			Source: clip.Source + " db",
			Link:   link,
		})
	}

	if len(matches) == 0 {
		return nil
	}

	sort.SliceStable(matches, func(i, j int) bool {
		return matches[i].Score > matches[j].Score
	})

	limit := 8
	if len(matches) > limit {
		matches = matches[:limit]
	}

	return matches
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

// enrichTimelineSegments enriches timeline segments with stock and artlist matches.
func enrichTimelineSegments(plan *TimelinePlan, dataDir string, req ScriptDocsRequest, repo *clips.Repository, ctx context.Context) {
	if plan == nil || len(plan.Segments) == 0 {
		return
	}

	for i := range plan.Segments {
		seg := &plan.Segments[i]
		// Match stock clips from DB
		stockMatches := matchStockForSegment(repo, ctx, req.Topic, *seg)
		seg.StockMatches = cloneScoredMatches(stockMatches)

		// Populate ArtlistMatches (artlist clips) and DriveMatches (stock drive clips)
		seg.ArtlistMatches = filterMatchesBySource(stockMatches, "artlist")
		seg.DriveMatches = filterMatchesBySource(stockMatches, "stock")
	}
}

// filterMatchesBySource filters matches by clip source (artlist/stock)
func filterMatchesBySource(matches []scoredMatch, source string) []scoredMatch {
	if len(matches) == 0 {
		return nil
	}
	filtered := make([]scoredMatch, 0, len(matches))
	for _, m := range matches {
		if m.Source == source+" db" {
			filtered = append(filtered, m)
		}
	}
	return filtered
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
