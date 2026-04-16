package artlistpipeline

import (
	"strings"

	"velox/go-master/internal/artlistdb"
	"velox/go-master/internal/service/scriptdocs"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// associateSentencesWithClips finds the best keyword for each sentence
// and searches Artlist for matching clips (preferring already-downloaded ones).
func (h *Handler) associateSentencesWithClips(sentences []string, maxClips int) []SentenceAssociation {
	var associations []SentenceAssociation

	for i, sentence := range sentences {
		sentenceLower := strings.ToLower(sentence)

		// Find best matching concept for this sentence
		bestConcept, bestKeyword := findBestConcept(sentenceLower)
		if bestConcept == "" {
			continue
		}

		// Check DB for clips for this term
		clips, foundInDB := h.artlistDB.GetClipsForTerm(bestConcept)

		if foundInDB && len(clips) > 0 {
			// Prefer already downloaded clips
			var downloadedClips []artlistdb.ArtlistClip
			for _, clip := range clips {
				if clip.Downloaded && clip.DriveFileID != "" {
					downloadedClips = append(downloadedClips, clip)
				}
			}

			if len(downloadedClips) > 0 {
				selectedClips := selectUniqueClips(downloadedClips, maxClips, bestConcept)
				associations = append(associations, SentenceAssociation{
					Sentence:    sentence,
					SentenceIdx: i,
					Keyword:     bestKeyword,
					ArtlistTerm: bestConcept,
					Clips:       selectedClips,
					ClipsNeeded: 0,
				})
				continue
			}

			// No downloaded clips yet — take the first N undownloaded clips
			limit := minInt(maxClips, len(clips))
			associations = append(associations, SentenceAssociation{
				Sentence:    sentence,
				SentenceIdx: i,
				Keyword:     bestKeyword,
				ArtlistTerm: bestConcept,
				Clips:       clips[:limit],
				ClipsNeeded: limit,
			})
			continue
		}

		// Not in DB — search Artlist dynamically
		if h.artlistSrc == nil {
			continue
		}

		searchResults, err := h.artlistSrc.SearchClips(bestConcept, maxClips*3)
		if err != nil || len(searchResults) == 0 {
			logger.Debug("No Artlist clips found for concept",
				zap.String("concept", bestConcept),
				zap.String("keyword", bestKeyword))
			continue
		}

		// Convert to ArtlistClip and save to DB
		var artlistClips []artlistdb.ArtlistClip
		for _, sr := range searchResults {
			artlistClips = append(artlistClips, artlistdb.ArtlistClip{
				ID:          sr.ID,
				VideoID:     sr.Filename,
				Title:       sr.Name,
				OriginalURL: sr.DownloadLink,
				URL:         sr.DownloadLink,
				Duration:    int(sr.Duration),
				Width:       sr.Width,
				Height:      sr.Height,
				Category:    sr.FolderPath,
				Tags:        sr.Tags,
			})
		}

		h.artlistDB.AddSearchResults(bestConcept, artlistClips)

		limit := minInt(maxClips, len(artlistClips))
		associations = append(associations, SentenceAssociation{
			Sentence:    sentence,
			SentenceIdx: i,
			Keyword:     bestKeyword,
			ArtlistTerm: bestConcept,
			Clips:       artlistClips[:limit],
			ClipsNeeded: limit,
		})
	}

	return associations
}

// findBestConcept finds the best concept and keyword for a sentence.
func findBestConcept(sentenceLower string) (string, string) {
	type match struct {
		concept string
		keyword string
		score   int
	}

	var bestMatch match

	for _, cm := range scriptdocs.GetConceptMap() {
		for _, kw := range cm.Keywords {
			kwLower := strings.ToLower(kw)
			if strings.Contains(sentenceLower, kwLower) {
				score := len(kw)
				if score > bestMatch.score {
					bestMatch = match{
						concept: cm.Term,
						keyword: kw,
						score:   score,
					}
				}
			}
		}
	}

	return bestMatch.concept, bestMatch.keyword
}

// selectUniqueClips selects clips using round-robin to avoid reusing same clips.
func selectUniqueClips(clips []artlistdb.ArtlistClip, maxClips int, term string) []artlistdb.ArtlistClip {
	if len(clips) == 0 {
		return nil
	}

	startIdx := 0 // Could track this in DB for true round-robin
	var selected []artlistdb.ArtlistClip
	for j := 0; j < maxClips && j < len(clips); j++ {
		idx := (startIdx + j) % len(clips)
		selected = append(selected, clips[idx])
	}
	return selected
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
