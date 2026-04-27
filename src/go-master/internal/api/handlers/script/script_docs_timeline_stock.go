package script

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/ml/ollama/types"
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

	// Use ListClips and score manually (more reliable)
	dbClips, err := repo.ListClips(ctx, "")
	if err != nil || len(dbClips) == 0 {
		return nil
	}

	type folderMatch struct {
		FolderID  string
		FolderPath string
		MaxScore  int
		BestTitle string
		Source    string
	}
	folderMatches := make(map[string]*folderMatch)

	// Score all clips and group by folder
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

		// KEY: We group strictly by FolderPath to ensure a single entry per physical folder
		groupKey := clip.FolderPath
		if groupKey == "" {
			groupKey = "root_" + clip.Source
		}

		if existing, ok := folderMatches[groupKey]; !ok || score > existing.MaxScore {
			folderMatches[groupKey] = &folderMatch{
				FolderID:   clip.FolderID,
				FolderPath: clip.FolderPath,
				MaxScore:   score,
				BestTitle:  clip.Name,
				Source:     clip.Source,
			}
		}
	}

	// Convert grouped folder matches to scoredMatch
	scored := make([]scoredMatch, 0, len(folderMatches))
	for _, fm := range folderMatches {
		link := ""
		// Prioritize FolderID for the link
		if fm.FolderID != "" {
			link = "https://drive.google.com/drive/folders/" + fm.FolderID
		}
		
		// The title must be the folder name for clarity, not the clip name
		title := fm.FolderPath
		if idx := strings.LastIndex(title, "/"); idx != -1 {
			title = title[idx+1:]
		}
		if title == "" {
			title = fm.BestTitle
		}

		scored = append(scored, scoredMatch{
			Title:  title,
			Score:  fm.MaxScore,
			Source: fm.Source + " db",
			Link:   link,
		})
	}

	// Sort and limit
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})
	if len(scored) > 8 {
		scored = scored[:8]
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

// enrichTimelineSegments enriches timeline segments with stock and artlist matches.
func enrichTimelineSegments(plan *TimelinePlan, dataDir string, req ScriptDocsRequest, repo *clips.Repository, ctx context.Context, gen *ollama.Generator, analysis *types.FullEntityAnalysis) {
	if plan == nil || len(plan.Segments) == 0 {
		return
	}

	for i := range plan.Segments {
		seg := &plan.Segments[i]

		// Use new chapter-based matching
		chapterMatches := MatchChapterClips(ctx, gen, repo, *seg, req.Topic)

		// Set stock matches from chapter matching
		if len(chapterMatches.AllMatches) > 0 {
			seg.StockMatches = cloneScoredMatches(chapterMatches.AllMatches)
		} else {
			// Fallback to old method
			stockMatches := matchStockForSegment(repo, ctx, req.Topic, *seg)
			seg.StockMatches = cloneScoredMatches(stockMatches)
		}

		// Populate ArtlistMatches and DriveMatches
		artlistFromDB := filterMatchesBySource(seg.StockMatches, "artlist")
		if len(artlistFromDB) == 0 {
			seg.ArtlistMatches = loadArtlistFromLegacyJSON(dataDir, req.Topic, *seg)
		} else {
			seg.ArtlistMatches = artlistFromDB
		}

		// If still no matches, add suggestions from analysis if available
		if len(seg.ArtlistMatches) == 0 && gen != nil && analysis != nil {
			for _, segEnt := range analysis.SegmentEntities {
				for phrase, keywords := range segEnt.ArtlistPhrases {
					match := false
					for _, k := range keywords {
						for _, sk := range seg.Keywords {
							if strings.Contains(strings.ToLower(sk), strings.ToLower(k)) {
								match = true
								break
							}
						}
						if match {
							break
						}
					}
					if match {
						tags := suggestArtlistSearchTags(ctx, gen, req, phrase, segEnt.SegmentText)
						if len(tags) > 0 {
							seg.ArtlistMatches = append(seg.ArtlistMatches, scoredMatch{
								Title:   phrase,
								Source:  "artlist_suggestion",
								Score:   50,
								Details: strings.Join(tags, ", "),
							})
						}
					}
				}
			}
		}

		seg.DriveMatches = filterMatchesBySource(seg.StockMatches, "stock", "clips", "stock_drive")
	}
}

// filterMatchesBySource filters matches by clip source (supports multiple sources)
func filterMatchesBySource(matches []scoredMatch, sources ...string) []scoredMatch {
	if len(matches) == 0 || len(sources) == 0 {
		return nil
	}
	filtered := make([]scoredMatch, 0, len(matches))
	for _, m := range matches {
		matchSource := strings.ToLower(m.Source)
		for _, src := range sources {
			if strings.Contains(matchSource, strings.ToLower(src)) {
				filtered = append(filtered, m)
				break
			}
		}
	}
	return filtered
}

func loadArtlistFromLegacyJSON(dataDir string, topic string, seg TimelineSegment) []scoredMatch {
	path := filepath.Join(dataDir, "artlist_stock_index.json")
	var index artlistIndex
	if err := readJSON(path, &index); err != nil {
		return nil
	}

	terms := collectTopicTerms(topic)
	terms = append(terms, seg.Keywords...)
	terms = append(terms, seg.Entities...)
	terms = uniqueStrings(terms)

	matches := make([]scoredMatch, 0, 8)
	seen := make(map[string]bool)

	for _, clip := range index.Clips {
		title := clip.DisplayName()
		if seen[title] {
			continue
		}

		score := scoreText(strings.ToLower(strings.Join([]string{
			title,
			clip.Filename,
			clip.Folder,
			clip.Category,
			strings.Join(clip.Tags, " "),
			clip.Source,
		}, " ")), terms)

		if score == 0 {
			continue
		}

		seen[title] = true
		matches = append(matches, scoredMatch{
			Title:   title,
			Source:  "artlist local index",
			Link:    clip.PickLink(),
			Score:   score,
			Details: strings.Join(clip.Tags, ", "),
		})
	}

	sort.SliceStable(matches, func(i, j int) bool {
		return matches[i].Score > matches[j].Score
	})

	if len(matches) > 4 {
		matches = matches[:4]
	}
	return matches
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
