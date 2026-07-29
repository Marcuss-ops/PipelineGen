package client

import (
	"regexp"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/linguistics"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

func fallbackEntityExtractionResult(segment string, segmentIndex, entityCount int) *asset.EntityExtractionResult {
	segment = strings.TrimSpace(segment)
	if entityCount <= 0 {
		entityCount = 2
	}
	result := &asset.EntityExtractionResult{
		SegmentIndex:     segmentIndex,
		FrasiImportanti:  []string{},
		EntitaSenzaTesto: make(map[string]string),
		NomiSpeciali:     []string{},
		ParoleImportanti: []string{},
		ArtlistPhrases:   []string{},
		Source:           "heuristic_fallback",
	}

	sentences := splitSentences(segment)
	for i, s := range sentences {
		if i >= 5 {
			break
		}
		s = strings.TrimSpace(s)
		if len(s) > 20 && len(s) < 300 {
			result.FrasiImportanti = append(result.FrasiImportanti, s)
		}
	}

	result.ParoleImportanti = fallbackImportantWords(segment, entityCount)
	result.NomiSpeciali = fallbackSpecialNames(segment, entityCount)
	result.ArtlistPhrases = fallbackArtlistPhrases(segment, entityCount)
	return result
}

func fallbackImportantPhrases(segment string, limit int) []string {
	if limit <= 0 {
		limit = 5
	}
	sentences := splitSentences(segment)
	var out []string
	seen := make(map[string]struct{})
	for _, s := range sentences {
		s = strings.TrimSpace(s)
		if s == "" || len(s) < 20 {
			continue
		}
		key := strings.ToLower(s)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimPhrase(s, 200))
		if len(out) >= limit {
			break
		}
	}
	return out
}

func fallbackSpecialNames(segment string, limit int) []string {
	if limit <= 0 {
		limit = 5
	}
	words := textutil.Tokenize(segment)
	seen := make(map[string]struct{})
	var out []string

	capPattern := regexp.MustCompile(`^[A-Z][a-z]+(?:\s+[A-Z][a-z]+)*$`)
	for _, w := range words {
		if len(w) < 3 || textutil.IsStopWord(strings.ToLower(w)) {
			continue
		}
		if !capPattern.MatchString(w) {
			continue
		}
		if isGenericImportantWord(strings.ToLower(w)) {
			continue
		}
		if _, ok := seen[strings.ToLower(w)]; ok {
			continue
		}
		seen[strings.ToLower(w)] = struct{}{}
		out = append(out, w)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func fallbackArtlistPhrases(segment string, limit int) []string {
	if limit <= 0 {
		limit = 5
	}
	if limit > 5 {
		limit = 5
	}
	profile := linguistics.DefaultLexicon().Resolve("fallback")
	words := textutil.Tokenize(segment)
	if len(words) < 4 {
		return nil
	}
	var out []string
	seen := make(map[string]struct{})
	// Skip first 2 words to avoid sentence-start function words
	startIdx := 0
	for i := 0; i < len(words) && i < 3; i++ {
		if !textutil.IsStopWord(strings.ToLower(words[i])) && !isGenericImportantWord(strings.ToLower(words[i])) {
			startIdx = i
			break
		}
	}
	for i := startIdx; i < len(words)-1 && len(out) < limit; i++ {
		w1 := strings.ToLower(words[i])
		w2 := strings.ToLower(words[i+1])

		// Skip if either word is a stop word
		if textutil.IsStopWord(w1) || textutil.IsStopWord(w2) {
			continue
		}
		if isGenericImportantWord(w1) || isGenericImportantWord(w2) {
			continue
		}
		// Skip if either word is a function word (article, preposition, etc.)
		if isFunctionWord(w1, profile) || isFunctionWord(w2, profile) {
			continue
		}
		// Skip if either word has apostrophe (contraction fragments)
		if strings.Contains(words[i], "'") || strings.Contains(words[i], "’") || strings.Contains(words[i+1], "'") || strings.Contains(words[i+1], "’") {
			continue
		}
		// Skip single-letter words
		if len(words[i]) <= 1 || len(words[i+1]) <= 1 {
			continue
		}

		phrase := w1 + " " + w2
		if _, ok := seen[phrase]; ok {
			continue
		}
		seen[phrase] = struct{}{}
		out = append(out, words[i]+" "+words[i+1])
	}
	return out
}

func extractSignificantWords(text string, limit int) []string {
	words := textutil.Tokenize(text)
	seen := make(map[string]struct{})
	var out []string
	for _, w := range words {
		wLower := strings.ToLower(w)
		if len(w) < 3 || textutil.IsStopWord(wLower) || isGenericImportantWord(wLower) {
			continue
		}
		if _, ok := seen[wLower]; ok {
			continue
		}
		seen[wLower] = struct{}{}
		out = append(out, w)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func fallbackImportantWords(segment string, limit int) []string {
	if limit <= 0 {
		limit = 5
	}
	return extractSignificantWords(segment, limit)
}
