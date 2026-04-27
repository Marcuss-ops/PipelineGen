package script

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/service/matching"
	"velox/go-master/pkg/models"
)

// ChapterClipMatch represents clip matches for a chapter/segment
type ChapterClipMatch struct {
	OpeningSentenceMatches []clipMatchResult
	ClosingSentenceMatches []clipMatchResult
	AllMatches             []scoredMatch
}

// clipMatchResult represents a clip match with metadata
type clipMatchResult struct {
	Clip        models.Clip
	Score       int
	Reason      string
	MatchedTags []string
}

// MatchChapterClips performs comprehensive clip matching for a timeline segment/chapter
func MatchChapterClips(
	ctx context.Context,
	gen *ollama.Generator,
	repo *clips.Repository,
	seg TimelineSegment,
	topic string,
	nodeScraperDir string,
) ChapterClipMatch {

	result := ChapterClipMatch{
		OpeningSentenceMatches: make([]clipMatchResult, 0),
		ClosingSentenceMatches: make([]clipMatchResult, 0),
		AllMatches:             make([]scoredMatch, 0),
	}

	// Collect all phrases to match
	phrases := collectSegmentPhrases(seg)
	if len(phrases) == 0 {
		return result
	}

	// Extract tags for each phrase using LLM
	phraseTags := make(map[string][]string)
	if gen != nil {
		for _, phrase := range phrases {
			tags := extractPhraseTags(ctx, gen, phrase, topic)
			phraseTags[phrase] = tags
		}
	}

	// For each phrase, find matching clips
	for _, phrase := range phrases {
		tags := phraseTags[phrase]
		matches := findClipsForPhrase(ctx, repo, phrase, tags, seg, nodeScraperDir)

		// Categorize matches
		for _, match := range matches {
			result.AllMatches = append(result.AllMatches, match)

			// Determine if this is for opening or closing sentence
			if strings.Contains(strings.ToLower(seg.OpeningSentence), strings.ToLower(phrase)) ||
				strings.Contains(strings.ToLower(phrase), strings.ToLower(seg.OpeningSentence)) {
				result.OpeningSentenceMatches = append(result.OpeningSentenceMatches, clipMatchResult{
					Clip:        matchToClip(match),
					Score:       match.Score,
					Reason:      "",
					MatchedTags: tags,
				})
			}

			if strings.Contains(strings.ToLower(seg.ClosingSentence), strings.ToLower(phrase)) ||
				strings.Contains(strings.ToLower(phrase), strings.ToLower(seg.ClosingSentence)) {
				result.ClosingSentenceMatches = append(result.ClosingSentenceMatches, clipMatchResult{
					Clip:        matchToClip(match),
					Score:       match.Score,
					Reason:      "",
					MatchedTags: tags,
				})
			}
		}
	}

	return result
}

// collectSegmentPhrases collects all phrases from a segment
func collectSegmentPhrases(seg TimelineSegment) []string {
	phrases := make([]string, 0, 4)
	if seg.OpeningSentence != "" {
		phrases = append(phrases, seg.OpeningSentence)
	}
	if seg.ClosingSentence != "" && seg.ClosingSentence != seg.OpeningSentence {
		phrases = append(phrases, seg.ClosingSentence)
	}
	return phrases
}

// extractPhraseTags uses LLM to extract relevant tags from a phrase
func extractPhraseTags(ctx context.Context, gen *ollama.Generator, phrase, topic string) []string {
	if gen == nil {
		return []string{}
	}

	client := gen.GetClient()
	if client == nil {
		return []string{}
	}

	prompt := fmt.Sprintf(`Extract 3-5 relevant search tags from this phrase for finding video clips.
The main topic is: %s

PHRASE: "%s"

Return ONLY a JSON array of tags, nothing else.
Example: ["boxing", "champion", "fight"]

JSON:`, topic, phrase)

	raw, err := client.GenerateWithOptions(ctx, "gemma3:4b", prompt, map[string]interface{}{
		"temperature": 0.1,
		"num_predict": 100,
	})
	if err != nil {
		return []string{}
	}

	// Parse JSON array
	tags := parseJSONTags(raw)
	if len(tags) > 0 {
		return tags
	}

	// Fallback: extract words from phrase
	return extractKeywordsFromPhrase(phrase)
}

// parseJSONTags parses a JSON array of tags from LLM response
func parseJSONTags(raw string) []string {
	cleaned := stripCodeFence(raw)
	jsonPayload := extractJSONObject(cleaned)
	if jsonPayload == "" {
		// Try to find array directly
		if start := strings.Index(cleaned, "["); start != -1 {
			if end := strings.Index(cleaned, "]"); end != -1 && end > start {
				jsonPayload = cleaned[start : end+1]
			}
		}
	}

	if jsonPayload == "" {
		return nil
	}

	var tags []string
	if err := json.Unmarshal([]byte(jsonPayload), &tags); err != nil {
		return nil
	}

	return tags
}

// extractKeywordsFromPhrase extracts keywords from phrase as fallback
func extractKeywordsFromPhrase(phrase string) []string {
	// Simple keyword extraction - can be enhanced
	words := strings.Fields(strings.ToLower(phrase))
	keywords := make([]string, 0, len(words))
	seen := make(map[string]bool)

	for _, word := range words {
		word = strings.Trim(word, ".,!?;:\"'")
		if len(word) > 3 && !seen[word] {
			seen[word] = true
			keywords = append(keywords, word)
		}
	}

	return keywords
}

// findClipsForPhrase finds clips matching a phrase using tags
func findClipsForPhrase(
	ctx context.Context,
	repo *clips.Repository,
	phrase string,
	tags []string,
	seg TimelineSegment,
	nodeScraperDir string,
) []scoredMatch {

	allTerms := make([]string, 0)
	allTerms = append(allTerms, tags...)
	allTerms = append(allTerms, seg.Keywords...)
	allTerms = append(allTerms, seg.Entities...)
	allTerms = uniqueStrings(allTerms)

	// First: Search in DB using tags
	dbMatches := searchClipsInDB(ctx, repo, allTerms, phrase, 10)
	if len(dbMatches) > 0 {
		return dbMatches
	}

	// Second: If no DB matches, search Artlist and save to DB
	artlistMatches := searchArtlistAndSave(ctx, repo, phrase, tags, seg, nodeScraperDir)
	if len(artlistMatches) > 0 {
		return artlistMatches
	}

	// Fallback: search DB with phrase directly
	return searchClipsInDB(ctx, repo, []string{phrase}, phrase, 5)
}

// searchClipsInDB searches for clips in the database using tags/terms
func searchClipsInDB(ctx context.Context, repo *clips.Repository, terms []string, phrase string, limit int) []scoredMatch {
	if repo == nil || len(terms) == 0 {
		return nil
	}

	clips, err := repo.SearchStockByKeywords(ctx, terms, limit*2)
	if err != nil || len(clips) == 0 {
		return nil
	}

	matches := make([]scoredMatch, 0, len(clips))
	for _, clip := range clips {
		score := scoreClipForPhrase(clip, phrase, terms)
		if score < 20 {
			continue
		}

		matches = append(matches, scoredMatch{
			Title:   clip.Name,
			Score:   int(score),
			Source:  clip.Source + " db",
		Link:    clip.DriveLink,
			Details: strings.Join(clip.Tags, ", "),
		})

		if len(matches) >= limit {
			break
		}
	}

	return matches
}

// scoreClipForPhrase scores a clip's relevance to a phrase
func scoreClipForPhrase(clip *models.Clip, phrase string, terms []string) float64 {
	clipText := strings.ToLower(strings.Join([]string{
		clip.Name,
		clip.Filename,
		clip.FolderPath,
		clip.Group,
		strings.Join(clip.Tags, " "),
		clip.Source,
	}, " "))

	phraseNorm := strings.ToLower(phrase)

	// Count matching terms
	hitCount := 0
	for _, term := range terms {
		if strings.Contains(clipText, strings.ToLower(term)) {
			hitCount++
		}
	}

	if hitCount == 0 {
		return 0
	}

	baseScore := (float64(hitCount) / float64(len(terms))) * 100

	// Boost for name/filename match
	boost := 0.0
	name := strings.ToLower(clip.Name)
	filename := strings.ToLower(strings.TrimSuffix(clip.Filename, filepath.Ext(clip.Filename)))

	if strings.Contains(phraseNorm, name) || strings.Contains(name, phraseNorm) {
		boost += matching.NameMatchBoost
	}
	if strings.Contains(phraseNorm, filename) || strings.Contains(filename, phraseNorm) {
		boost += matching.FilenameMatchBoost
	}

	score := baseScore + boost
	if score > 100 {
		score = 100
	}

	return score
}

// searchArtlistAndSave searches Artlist for clips and saves them to DB
func searchArtlistAndSave(
	ctx context.Context,
	repo *clips.Repository,
	phrase string,
	tags []string,
	seg TimelineSegment,
	nodeScraperDir string,
) []scoredMatch {

	if repo == nil || nodeScraperDir == "" {
		return nil
	}

	// Use first tag as search term, or fallback to phrase
	searchTerm := phrase
	if len(tags) > 0 {
		searchTerm = tags[0]
	}
	if len(tags) > 1 {
		searchTerm = tags[0] + " " + tags[1]
	}

	// Call Artlist scraper
	scrapedClips, err := fetchFromArtlistScraper(ctx, searchTerm, nodeScraperDir)
	if err != nil || len(scrapedClips) == 0 {
		return nil
	}

	// Save to DB and build matches
	matches := make([]scoredMatch, 0, len(scrapedClips))
	for _, clip := range scrapedClips {
		// Add tags from phrase analysis
		if len(tags) > 0 {
			clip.Tags = append(clip.Tags, tags...)
		}

		// Save to DB
		if err := repo.UpsertClip(ctx, &clip); err == nil {
			matches = append(matches, scoredMatch{
				Title:   clip.Name,
				Score:   80, // High score for Artlist matches
				Source:  clip.Source + " live scrape",
				Link:    resolveArtlistClipLink(clip, nodeScraperDir),
				Details: strings.Join(clip.Tags, ", "),
			})
		}
	}

	return matches
}

// matchToClip converts a scoredMatch to a models.Clip
func matchToClip(match scoredMatch) models.Clip {
	return models.Clip{
		Name:      match.Title,
		DriveLink: match.Link,
		Source:    strings.TrimSuffix(match.Source, " db"),
		Tags:      strings.Split(match.Details, ", "),
	}
}
