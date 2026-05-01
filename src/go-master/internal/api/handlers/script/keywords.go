package script

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/ml/ollama/types"
	"velox/go-master/pkg/textutil"
)

// extractDynamicKeywords chiede all'LLM di generare esattamente 3 keyword di ricerca efficaci
func extractDynamicKeywords(ctx context.Context, gen *ollama.Generator, subject, narrative string) []string {
	if gen == nil || gen.GetClient() == nil {
		return extractSearchKeywords(subject, narrative)
	}

	prompt := fmt.Sprintf(`Given this video segment, provide exactly 3 short and effective search keywords for stock footage.
Subject: %s
Narrative: %s

Return ONLY the three keywords separated by a comma. Example: wood fired pizza, italian chef cooking, pizza dough kneading`, subject, narrative)

	res, err := gen.GetClient().GenerateWithOptions(ctx, types.DefaultModel, prompt, map[string]interface{}{
		"temperature": 0.0,
		"num_predict": 45,
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

	if len(result) > 3 {
		result = result[:3]
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
		if len(tokens) > 3 {
			return tokens[:3]
		}
		return tokens
	}
	allTokens := textutil.Tokenize(narrative)
	var filtered []string
	for _, t := range allTokens {
		if len(t) > 3 {
			filtered = append(filtered, t)
		}
		if len(filtered) >= 3 {
			break
		}
	}
	return filtered
}

// collectTopicTerms estrae i termini principali dal topic per il matching
func collectTopicTerms(topic string) []string {
	seen := make(map[string]struct{})
	add := func(text string) {
		for _, term := range textutil.Tokenize(text) {
			if len(term) < 3 || textutil.IsStopWord(term) {
				continue
			}
			seen[term] = struct{}{}
		}
	}
	add(topic)
	terms := make([]string, 0, len(seen))
	for term := range seen {
		terms = append(terms, term)
	}
	sort.Strings(terms)
	return terms
}
