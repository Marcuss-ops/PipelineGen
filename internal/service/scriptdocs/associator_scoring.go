package scriptdocs

import (
	"context"
	"slices"
	"strings"
	"unicode"
)

type SignalClass int

const (
	SignalClassVisual SignalClass = iota
	SignalClassSemantic
	SignalClassFallback
)

type ScoredSignal struct {
	Keyword  string
	Score    int
	Class    SignalClass
	Concept  string
	BaseConf float64
}

// analyzeAndScore uses semantic similarity and LLM suggestions instead of hardcoded keyword lists.
func analyzeAndScore(fullContext, phrase string, conceptMap []clipConcept, domainContext string, fraseEntities []string, semanticScorer *SemanticScorer, visualSearchTerm string) []ScoredSignal {
	fraseLower := strings.ToLower(fullContext)
	if strings.TrimSpace(fraseLower) == "" {
		return nil
	}

	phraseTokens := tokenizeWords(strings.ToLower(phrase))
	var results []ScoredSignal

	for _, cm := range conceptMap {
		// 1. Basic Keyword Match (very light)
		score, bestKw := scoreConceptForPhrase(fraseLower, phraseTokens, cm)
		
		// 2. Semantic Scorer (Core logic)
		if semanticScorer != nil {
			semScore := semanticScorer.ScoreSimilarity(context.Background(), phrase, cm.Term)
			if semScore >= 4 {
				score += semScore
				if bestKw == "" {
					bestKw = cm.Term
				}
			}
		}

		// 3. Director Bonus (LLM Suggestion confirmation)
		if visualSearchTerm != "" && (strings.Contains(cm.Term, visualSearchTerm) || strings.Contains(visualSearchTerm, cm.Term)) {
			score += 10 // Strong bonus for director confirmation
			if bestKw == "" {
				bestKw = visualSearchTerm
			}
		}

		if score <= 0 {
			continue
		}

		kwLower := normalizeKeyword(bestKw)
		if kwLower == "" {
			kwLower = normalizeKeyword(cm.Term)
		}

		results = append(results, ScoredSignal{
			Keyword:  kwLower,
			Score:    score,
			Class:    SignalClassSemantic,
			Concept:  cm.Term,
			BaseConf: cm.BaseConf,
		})
	}

	// Sort by score descending
	slices.SortFunc(results, func(a, b ScoredSignal) int {
		return b.Score - a.Score
	})

	return results
}

type conceptScore struct {
	cm          ClipConcept
	score       int
	bestKeyword string
}

func (s *ScriptDocService) scoreConcepts(ctx context.Context, frase, contextLower string, visualHistory []string, visualSearchTerm string) []conceptScore {
	signals := analyzeAndScore(contextLower, frase, conceptMap, s.currentTopic, nil, s.semanticScorer, visualSearchTerm)
	
	var scores []conceptScore
	for _, sig := range signals {
		// Penalty for history
		finalScore := sig.Score
		for _, prev := range visualHistory {
			if sig.Concept == prev {
				finalScore -= 12
			}
		}

		scores = append(scores, conceptScore{
			cm:          ClipConcept{Term: sig.Concept, BaseConf: sig.BaseConf},
			score:       finalScore,
			bestKeyword: sig.Keyword,
		})
	}
	return scores
}

func scoreConceptForPhrase(fraseLower string, phraseTokens []string, cm clipConcept) (int, string) {
	tokens := tokenizeWords(fraseLower)
	if len(tokens) == 0 {
		return 0, ""
	}
	tokenSet := make(map[string]bool, len(tokens))
	for _, t := range tokens {
		tokenSet[t] = true
	}

	bestKeyword := ""
	matchScore := 0
	for _, kw := range cm.Keywords {
		kwLower := normalizeKeyword(kw)
		if len(kwLower) < 3 {
			continue
		}

		if tokenSet[kwLower] {
			matchScore += 5
			bestKeyword = kwLower
		}
	}
	return matchScore, bestKeyword
}

func normalizeKeyword(raw string) string {
	return strings.TrimFunc(strings.ToLower(strings.TrimSpace(raw)), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func isUsableDynamicKeyword(raw string) bool {
	kw := normalizeKeyword(raw)
	if len(kw) < 3 {
		return false
	}
	hasLetter := false
	for _, r := range kw {
		if unicode.IsLetter(r) {
			hasLetter = true
			break
		}
	}
	return hasLetter
}

// Stubs for remaining logic to keep compatibility without hardcoded lists
func extractEntitiesFromPhrase(frase string) []string { return nil }
func applyEntityPenalty(conceptTerm string, fraseEntities []string) int { return 0 }
func isEntityMismatch(conceptTerm string, fraseEntities []string) bool { return false }
func classifyVisualIntent(frase string) int { return 0 }
