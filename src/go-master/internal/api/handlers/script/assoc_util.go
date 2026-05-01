package script

import (
	"context"
	"fmt"
	"math"
	"strings"

	"velox/go-master/internal/matching"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/ml/ollama/types"
	"velox/go-master/pkg/models"
	"velox/go-master/pkg/textutil"
)

// normalizeMatchText pulisce e normalizza il testo per il matching
func normalizeMatchText(text string) string {
	return textutil.Normalize(text)
}

// matchTokens divide il testo in token significativi
func matchTokens(text string) []string {
	return textutil.Tokenize(text)
}

// calcola lo score di similarità tra due insiemi di token
func calculateTokenScore(queryTokens, targetTokens []string) int {
	return matching.CalculateTokenScore(queryTokens, targetTokens)
}

// extractDynamicKeywords chiede all'LLM di generare esattamente 2 keyword di ricerca efficaci
func extractDynamicKeywords(ctx context.Context, gen *ollama.Generator, subject, narrative string) []string {
	if gen == nil || gen.GetClient() == nil {
		return extractSearchKeywords(subject, narrative)
	}

	prompt := fmt.Sprintf(`Given this video segment, provide exactly 2 short and effective search keywords for stock footage.
Subject: %s
Narrative: %s

Return ONLY the two keywords separated by a comma. Example: boxing match, mike tyson training`, subject, narrative)

	res, err := gen.GetClient().GenerateWithOptions(ctx, types.DefaultModel, prompt, map[string]interface{}{
		"temperature": 0.0,
		"num_predict": 30,
	})
	if err != nil {
		return extractSearchKeywords(subject, narrative)
	}

	parts := strings.Split(textutil.StripCodeFence(res), ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}

	if len(result) > 2 {
		result = result[:2]
	}
	if len(result) == 0 {
		return extractSearchKeywords(subject, narrative)
	}
	return result
}

// extractSearchKeywords (euristica fallback)
func extractSearchKeywords(subject, narrative string) []string {
	tokens := textutil.Tokenize(subject)
	if len(tokens) > 0 {
		if len(tokens) > 2 {
			return tokens[:2]
		}
		return tokens
	}
	allTokens := textutil.Tokenize(narrative)
	var filtered []string
	for _, t := range allTokens {
		if len(t) > 3 {
			filtered = append(filtered, t)
		}
		if len(filtered) >= 2 {
			break
		}
	}
	return filtered
}

func roundSeconds(f float64) float64 {
	return math.Round(f*100) / 100
}

func preferredCandidateBoost(segment *TimelineSegment, candidatePath, candidateLink, candidateTitle string) int {
	if segment == nil {
		return 0
	}

	preferred := append([]string{}, segment.PreferredStockPaths...)
	if len(preferred) == 0 {
		return 0
	}

	candidatePath = strings.ToLower(strings.TrimSpace(candidatePath))
	candidateLink = strings.ToLower(strings.TrimSpace(candidateLink))
	candidateTitle = strings.ToLower(strings.TrimSpace(candidateTitle))

	for _, pref := range preferred {
		pref = strings.ToLower(strings.TrimSpace(pref))
		if pref == "" {
			continue
		}
		if candidatePath != "" && strings.Contains(candidatePath, pref) {
			return 35
		}
		if candidateLink != "" && strings.Contains(candidateLink, pref) {
			return 35
		}
		if candidateTitle != "" && strings.Contains(candidateTitle, pref) {
			return 20
		}
	}

	return 0
}

func segmentAssociationSubject(segment *TimelineSegment) string {
	if segment == nil {
		return ""
	}
	return firstNonEmpty(segment.CanonicalSubject, segment.Subject)
}

func segmentAssociationKeywords(segment *TimelineSegment) []string {
	if segment == nil {
		return nil
	}
	if len(segment.CanonicalKeywords) > 0 {
		return segment.CanonicalKeywords
	}
	return segment.Keywords
}

func segmentAssociationEntities(segment *TimelineSegment) []string {
	if segment == nil {
		return nil
	}
	if len(segment.CanonicalEntities) > 0 {
		return segment.CanonicalEntities
	}
	return segment.Entities
}

func modelClipsToScoredMatches(clips []models.Clip, details string, source string, link string) []scoredMatch {
	matches := make([]scoredMatch, 0, len(clips))
	for _, c := range clips {
		l := link
		if l == "" {
			l = c.ExternalURL
		}
		matches = append(matches, scoredMatch{
			Title:   c.Name,
			Path:    c.LocalPath,
			Score:   100,
			Source:  source,
			Link:    l,
			Details: details,
		})
	}
	return matches
}
