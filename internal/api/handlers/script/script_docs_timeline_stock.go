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
	"velox/go-master/pkg/models"
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
		FolderID   string
		FolderPath string
		MaxScore   int
		BestTitle  string
		Source     string
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
	if len(scored) > 1 {
		scored = scored[:1] // Limit to exactly ONE drive folder link per timestamp
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
func enrichTimelineSegments(plan *TimelinePlan, dataDir string, req ScriptDocsRequest, repo *clips.Repository, ctx context.Context, gen *ollama.Generator, analysis *types.FullEntityAnalysis, nodeScraperDir string) {
	if plan == nil || len(plan.Segments) == 0 {
		return
	}

	for i := range plan.Segments {
		seg := &plan.Segments[i]

		// Always use folder-level matching for StockMatches
		stockMatches := matchStockForSegment(repo, ctx, req.Topic, *seg)
		seg.StockMatches = cloneScoredMatches(stockMatches)

		// Compute a single Artlist match per segment using the Artlist DB first,
		// then falling back to the live scraper and local persistence.
		seg.ArtlistMatches = matchArtlistForSegment(ctx, repo, *seg, analysis, nodeScraperDir)

		// Set DriveMatches to a single best folder-level match only
		seg.DriveMatches = pickTopScoredMatches(filterMatchesBySource(seg.StockMatches, "stock", "clips", "stock_drive"), 1)
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

// pickTopScoredMatches sorts matches by score and returns the top N items.
func pickTopScoredMatches(matches []scoredMatch, limit int) []scoredMatch {
	if len(matches) == 0 || limit <= 0 {
		return nil
	}

	cloned := make([]scoredMatch, len(matches))
	copy(cloned, matches)
	sort.SliceStable(cloned, func(i, j int) bool {
		if cloned[i].Score == cloned[j].Score {
			return strings.ToLower(cloned[i].Title) < strings.ToLower(cloned[j].Title)
		}
		return cloned[i].Score > cloned[j].Score
	})
	if len(cloned) > limit {
		cloned = cloned[:limit]
	}
	return cloned
}

func loadArtlistFromLegacyJSON(dataDir string, topic string, seg TimelineSegment) []scoredMatch {
	// Legacy JSON loading disabled - use DB only
	return nil
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

func matchArtlistForSegment(ctx context.Context, repo *clips.Repository, seg TimelineSegment, analysis *types.FullEntityAnalysis, nodeScraperDir string) []scoredMatch {
	terms := uniqueStrings(append([]string{}, seg.Keywords...))
	terms = append(terms, seg.Entities...)
	terms = append(terms, collectTopicTerms(seg.OpeningSentence)...)
	terms = append(terms, collectTopicTerms(seg.ClosingSentence)...)

	candidatePhrases := collectArtlistCandidatePhrases(analysis, seg)

	if analysis != nil {
		for _, extracted := range analysis.SegmentEntities {
			for phrase, keywords := range extracted.ArtlistPhrases {
				if artlistPhraseMatchesSegment(phrase, seg) {
					terms = append(terms, keywords...)
					terms = append(terms, collectTopicTerms(phrase)...)
				}
			}
		}
	}

	terms = uniqueStrings(terms)
	if len(terms) == 0 && len(candidatePhrases) == 0 {
		return nil
	}

	if len(candidatePhrases) == 0 {
		candidatePhrases = []string{seg.OpeningSentence, seg.ClosingSentence}
	}
	candidatePhrases = uniqueStrings(candidatePhrases)

	if strings.TrimSpace(nodeScraperDir) != "" {
		artlistDBClient := NewArtlistDBClient(nodeScraperDir)
		if artlistDBClient != nil {
			if len(candidatePhrases) > 0 {
				for _, phrase := range candidatePhrases {
					phraseKeywords := collectTopicTerms(phrase)
					if len(phraseKeywords) == 0 {
						phraseKeywords = terms
					}
					if matches, err := artlistDBClient.SearchClipsByKeywords(phraseKeywords, 5); err == nil && len(matches) > 0 {
						best := pickTopScoredMatches(matches, 1)
						if len(best) > 0 && strings.TrimSpace(best[0].Link) != "" {
							best[0].Title = phrase
							best[0].Details = best[0].Source
							return best
						}
					}
				}
			}
			if matches, err := artlistDBClient.SearchClipsByKeywords(terms, 5); err == nil && len(matches) > 0 {
				best := pickTopScoredMatches(matches, 1)
				if len(best) > 0 && strings.TrimSpace(best[0].Link) != "" {
					best[0].Title = firstNonEmptyString(candidatePhrases)
					best[0].Details = best[0].Source
					return best
				}
			}
		}
	}

	if repo != nil {
		if clips, err := repo.SearchStockByKeywords(ctx, terms, 8); err == nil && len(clips) > 0 {
			artlistOnly := filterMatchesBySource(clipMatchesToScored(clips, nodeScraperDir), "artlist")
			best := pickTopScoredMatches(artlistOnly, 1)
			if len(best) > 0 && strings.TrimSpace(best[0].Link) != "" {
				best[0].Title = firstNonEmptyString(candidatePhrases)
				return best
			}
		}
	}

	if strings.TrimSpace(nodeScraperDir) == "" {
		return nil
	}

	searchTerm := strings.Join(terms, " ")
	if len(terms) > 2 {
		searchTerm = strings.Join(terms[:2], " ")
	}

	scrapedClips, err := fetchFromArtlistScraper(ctx, searchTerm, nodeScraperDir)
	if err != nil || len(scrapedClips) == 0 {
		return nil
	}

	matches := make([]scoredMatch, 0, len(scrapedClips))
	for _, clip := range scrapedClips {
		for _, term := range terms {
			if term != "" {
				clip.Tags = append(clip.Tags, term)
			}
		}
		if repo != nil {
			_ = repo.UpsertClip(ctx, &clip)
		}

		link := resolveArtlistClipLink(clip, nodeScraperDir)
		if strings.TrimSpace(link) == "" {
			continue
		}

		matches = append(matches, scoredMatch{
			Title:   firstNonEmptyString(candidatePhrases),
			Score:   90,
			Source:  "artlist live scrape",
			Link:    link,
			Details: clip.Name,
		})
	}

	return pickTopScoredMatches(matches, 1)
}

func collectArtlistCandidatePhrases(analysis *types.FullEntityAnalysis, seg TimelineSegment) []string {
	if analysis == nil {
		return nil
	}

	var phrases []string
	for _, extracted := range analysis.SegmentEntities {
		for phrase, keywords := range extracted.ArtlistPhrases {
			phrase = strings.TrimSpace(phrase)
			if phrase == "" {
				continue
			}
			if len(keywords) == 0 {
				phrases = append(phrases, phrase)
				continue
			}
			phrases = append(phrases, phrase)
		}
		if len(phrases) == 0 {
			for _, phrase := range extracted.FrasiImportanti {
				phrase = strings.TrimSpace(phrase)
				if phrase != "" {
					phrases = append(phrases, phrase)
				}
			}
		}
	}

	return uniqueStrings(phrases)
}

func resolveArtlistClipLink(clip models.Clip, nodeScraperDir string) string {
	if strings.TrimSpace(clip.ExternalURL) != "" {
		return strings.TrimSpace(clip.ExternalURL)
	}
	if strings.TrimSpace(clip.DriveLink) != "" {
		return strings.TrimSpace(clip.DriveLink)
	}

	if strings.TrimSpace(nodeScraperDir) == "" || strings.TrimSpace(clip.ID) == "" {
		return ""
	}

	client := NewArtlistDBClient(nodeScraperDir)
	if client == nil {
		return ""
	}

	if link, err := client.LookupClipURLByVideoID(clip.ID); err == nil && strings.TrimSpace(link) != "" {
		return link
	}

	keywords := collectTopicTerms(strings.Join([]string{
		clip.Name,
		clip.Filename,
		clip.Metadata,
	}, " "))
	if len(keywords) > 0 {
		if matches, err := client.SearchClipsByKeywords(keywords, 1); err == nil {
			if best := selectBestMatchLink(matches); strings.TrimSpace(best) != "" {
				return best
			}
		}
	}

	return ""
}

func artlistPhraseMatchesSegment(phrase string, seg TimelineSegment) bool {
	phrase = strings.ToLower(strings.TrimSpace(phrase))
	if phrase == "" {
		return false
	}

	haystack := strings.ToLower(strings.Join([]string{
		seg.OpeningSentence,
		seg.ClosingSentence,
		strings.Join(seg.Keywords, " "),
		strings.Join(seg.Entities, " "),
	}, " "))
	if strings.Contains(haystack, phrase) {
		return true
	}

	if len(seg.Keywords) == 0 && len(seg.Entities) == 0 && strings.TrimSpace(seg.OpeningSentence) == "" && strings.TrimSpace(seg.ClosingSentence) == "" {
		return true
	}

	return false
}
