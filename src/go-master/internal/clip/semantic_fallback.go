package clip

import (
	"strings"

	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// getFallbackClips returns generic/b-roll clips when no specific match is found
func (s *SemanticSuggester) getFallbackClips(sentence string, maxResults int, _ float64, mediaType string) []SuggestionResult {
	index := s.indexer.GetIndex()
	if index == nil || len(index.Clips) == 0 {
		return nil
	}

	fallbackGroups := []string{"broll", "general", "stock"}

	var fallbackClips []IndexedClip
	for _, group := range fallbackGroups {
		for _, clip := range index.Clips {
			if mediaType != "" && !strings.EqualFold(clip.MediaType, mediaType) {
				continue
			}
			if strings.EqualFold(clip.Group, group) {
				if GlobalUsageTracker.GetPenalty(clip.ID) > 15 {
					continue
				}
				fallbackClips = append(fallbackClips, clip)
				if len(fallbackClips) >= maxResults {
					break
				}
			}
		}
		if len(fallbackClips) >= maxResults {
			break
		}
	}

	if len(fallbackClips) == 0 {
		return nil
	}

	var results []SuggestionResult
	for _, clip := range fallbackClips {
		results = append(results, SuggestionResult{
			Clip:        clip,
			Score:       5,
			MatchType:   "fallback_generic",
			MatchTerms:  []string{"generic"},
			MatchReason: "Generic fallback clip (no specific match found)",
		})
	}

	logger.Debug("Fallback clips returned",
		zap.Int("count", len(results)),
		zap.Strings("groups_used", fallbackGroups))

	return results
}
