// Package clip provides clip management functionality for the VeloxEditing system.
package clip

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"velox/go-master/internal/nlp"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// SemanticSuggester provides intelligent clip suggestions based on script/context matching
type SemanticSuggester struct {
	indexer *Indexer
}

// NewSemanticSuggester creates a new semantic suggester
func NewSemanticSuggester(indexer *Indexer) *SemanticSuggester {
	return &SemanticSuggester{
		indexer: indexer,
	}
}

// SuggestForSentence finds clips that match a sentence from the script
func (s *SemanticSuggester) SuggestForSentence(ctx context.Context, sentence string, maxResults int, minScore float64, mediaType string) []SuggestionResult {
	startTime := time.Now()

	normalizedSentence := normalizeSentence(sentence)
	cacheKey := buildCacheKey("sentence", normalizedSentence, maxResults, minScore, mediaType)

	if minScore <= 0 {
		minScore = DefaultMinScore
	}

	if cached, found := s.indexer.GetCache().Get(cacheKey); found {
		if results, ok := cached.([]SuggestionResult); ok {
			return results
		}
	}

	index := s.indexer.GetIndex()
	if index == nil {
		index = &ClipIndex{}
	}

	keywords := nlp.ExtractKeywords(sentence, 20)
	entities := s.extractEntities(sentence)

	logger.Debug("Extracted keywords for sentence",
		zap.String("sentence", sentence),
		zap.Int("keywords", len(keywords)),
		zap.Int("entities", len(entities)))

	var suggestions []SuggestionResult

	for _, clip := range index.Clips {
		if mediaType != "" && !strings.EqualFold(clip.MediaType, mediaType) {
			continue
		}
		score, matchDetails := s.scoreClipForSentence(clip, sentence, keywords, entities)

		if score >= minScore {
			suggestions = append(suggestions, SuggestionResult{
				Clip:        clip,
				Score:       score,
				MatchType:   matchDetails.MatchType,
				MatchTerms:  matchDetails.MatchTerms,
				MatchReason: matchDetails.Reason,
			})
		}
	}

	if s.indexer.artlistSrc != nil {
		artlistClips, err := s.indexer.artlistSrc.SearchClips(sentence, maxResults)
		if err == nil {
			for _, clip := range artlistClips {
				score, matchDetails := s.scoreClipForSentence(clip, sentence, keywords, entities)

				if score >= minScore {
					suggestions = append(suggestions, SuggestionResult{
						Clip:        clip,
						Score:       score,
						MatchType:   matchDetails.MatchType,
						MatchTerms:  matchDetails.MatchTerms,
						MatchReason: matchDetails.Reason,
					})
				}
			}
		}
	}

	sort.Slice(suggestions, func(i, j int) bool {
		return suggestions[i].Score > suggestions[j].Score
	})

	for i := range suggestions {
		penalty := GlobalUsageTracker.GetPenalty(suggestions[i].Clip.ID)
		if penalty > 0 {
			suggestions[i].Score -= penalty
			if suggestions[i].Score < 0 {
				suggestions[i].Score = 0
			}
			suggestions[i].MatchReason += fmt.Sprintf("; usage_penalty:-%.0f", penalty)
		}
	}

	sort.Slice(suggestions, func(i, j int) bool {
		return suggestions[i].Score > suggestions[j].Score
	})

	if maxResults > 0 && len(suggestions) > maxResults {
		suggestions = suggestions[:maxResults]
	}

	if len(suggestions) == 0 {
		suggestions = s.getFallbackClips(sentence, maxResults, minScore, mediaType)
	}

	var clipIDs []string
	for _, s := range suggestions {
		clipIDs = append(clipIDs, s.Clip.ID)
	}
	GlobalUsageTracker.RecordMultipleUsage(clipIDs)

	s.indexer.GetCache().Set(cacheKey, suggestions)

	logger.Info("Semantic suggestion completed",
		zap.String("sentence", sentence),
		zap.Int("suggestions", len(suggestions)),
		zap.Duration("duration", time.Since(startTime)))

	return suggestions
}

// SuggestForScript finds clips for an entire script, sentence by sentence
func (s *SemanticSuggester) SuggestForScript(ctx context.Context, script string, maxResultsPerSentence int, minScore float64, mediaType string) []ScriptSuggestion {
	startTime := time.Now()

	sentences := s.splitIntoSentences(script)

	logger.Info("Processing script for clip suggestions",
		zap.Int("sentences", len(sentences)),
		zap.String("media_type", mediaType))

	type sentenceResult struct {
		index       int
		sentence    string
		suggestions []SuggestionResult
	}

	resultsCh := make(chan sentenceResult, len(sentences))

	var wg sync.WaitGroup
	for i, sentence := range sentences {
		if len(sentence) < 10 {
			continue
		}

		wg.Add(1)
		go func(idx int, sent string) {
			defer wg.Done()
			suggestions := s.SuggestForSentence(ctx, sent, maxResultsPerSentence, minScore, mediaType)
			resultsCh <- sentenceResult{index: idx, sentence: sent, suggestions: suggestions}
		}(i, sentence)
	}

	go func() {
		wg.Wait()
		close(resultsCh)
	}()

	resultsMap := make(map[int]sentenceResult)
	for result := range resultsCh {
		resultsMap[result.index] = result
	}

	var scriptSuggestions []ScriptSuggestion
	for i, sentence := range sentences {
		if len(sentence) < 10 {
			continue
		}
		if result, ok := resultsMap[i]; ok && len(result.suggestions) > 0 {
			scriptSuggestions = append(scriptSuggestions, ScriptSuggestion{
				Sentence:    sentence,
				Suggestions: result.suggestions,
				BestScore:   result.suggestions[0].Score,
			})
		}
	}

	logger.Info("Script processing completed",
		zap.Int("sentences_processed", len(sentences)),
		zap.Int("sentences_with_matches", len(scriptSuggestions)),
		zap.Duration("duration", time.Since(startTime)))

	return scriptSuggestions
}
