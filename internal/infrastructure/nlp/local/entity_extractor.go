// Package local provides deterministic, CPU-only semantic extraction.
package local

import (
	"context"
	"regexp"
	"sort"
	"strings"

	adapters "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// Extractor is deliberately conservative: it only emits text found in the
// input and never calls a model or an external provider.
type Extractor struct{}

func NewExtractor() *Extractor { return &Extractor{} }

var (
	properNameRE = regexp.MustCompile(`(?:\b[\p{Lu}][\p{L}'’-]+)(?:\s+[\p{Lu}][\p{L}'’-]+)+`)
	acronymRE    = regexp.MustCompile(`\b[A-Z]{2,8}\b`)
	yearRE       = regexp.MustCompile(`\b(?:1[89]\d{2}|20\d{2})\b`)
	wordRE       = regexp.MustCompile(`[\p{L}\p{M}]+(?:['’][\p{L}\p{M}]+)?`)
	sentenceRE   = regexp.MustCompile(`[^.!?]+(?:[.!?]+|$)`)
)

var knownPlaces = map[string]struct{}{
	"las vegas": {}, "stati uniti": {}, "united states": {}, "new york": {}, "roma": {}, "parigi": {}, "londra": {}, "london": {}, "los angeles": {},
}

func (e *Extractor) ExtractEntities(_ context.Context, req scriptpkg.EntityExtractionRequest) (*scriptpkg.EntityResult, error) {
	text := strings.TrimSpace(req.Text)
	result := &scriptpkg.EntityResult{Persons: []scriptpkg.Entity{}, Places: []scriptpkg.Entity{}, Concepts: []scriptpkg.Entity{}, ImportantPhrases: []string{}, ImportantWords: []string{}, ArtlistPhrases: []string{}}
	if text == "" {
		return result, nil
	}
	seen := map[string]struct{}{}
	for _, match := range properNameRE.FindAllString(text, -1) {
		value := strings.TrimSpace(trimEntityPrefix(match))
		kind := classifyName(value)
		appendEntity(result, seen, kind, value, 0.90)
	}
	for _, match := range acronymRE.FindAllString(text, -1) {
		if strings.EqualFold(match, "I") {
			continue
		}
		appendEntity(result, seen, "ORG", match, 0.86)
	}
	for _, match := range yearRE.FindAllString(text, -1) {
		appendEntity(result, seen, "DATE", match, 0.99)
	}

	// One and only one phrase is selected for each scene. The ranker prefers
	// a sentence carrying an entity, a number, and informative vocabulary.
	if phrase := bestSentence(text); phrase != "" {
		result.ImportantPhrases = []string{phrase}
	}
	result.ImportantWords = importantWords(text, result, req.EntityCount, req.Language)
	for _, word := range result.ImportantWords {
		result.Concepts = append(result.Concepts, scriptpkg.Entity{Value: word, Type: "KEYWORD", Score: 0.70})
	}
	return result, nil
}

func classifyName(value string) string {
	lower := strings.ToLower(strings.TrimSpace(trimEntityPrefix(value)))
	if _, ok := knownPlaces[lower]; ok {
		return "GPE"
	}
	if strings.Contains(lower, " council") || strings.Contains(lower, " federation") || strings.Contains(lower, " association") {
		return "ORG"
	}
	return "PERSON"
}

func trimEntityPrefix(value string) string {
	parts := strings.Fields(strings.TrimSpace(value))
	for len(parts) > 0 {
		switch strings.ToLower(parts[0]) {
		case "a", "ad", "al", "alla", "da", "dal", "dalla", "di", "il", "la", "nel", "nella", "in", "the":
			parts = parts[1:]
		default:
			return strings.Join(parts, " ")
		}
	}
	return ""
}

func appendEntity(result *scriptpkg.EntityResult, seen map[string]struct{}, kind, value string, score float32) {
	key := strings.ToUpper(kind) + "\x00" + strings.ToLower(strings.TrimSpace(value))
	if strings.TrimSpace(value) == "" {
		return
	}
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	entity := scriptpkg.Entity{Value: strings.TrimSpace(value), Type: kind, Score: score}
	switch kind {
	case "PERSON":
		result.Persons = append(result.Persons, entity)
	case "GPE", "ORG":
		result.Places = append(result.Places, entity)
	default:
		result.Concepts = append(result.Concepts, entity)
	}
}

func bestSentence(text string) string {
	best, bestScore := "", -1
	for _, raw := range sentenceRE.FindAllString(text, -1) {
		phrase := strings.TrimSpace(raw)
		if phrase == "" {
			continue
		}
		words := wordRE.FindAllString(phrase, -1)
		if len(words) == 0 {
			continue
		}
		score := len(words)
		if properNameRE.MatchString(phrase) {
			score += 8
		}
		if yearRE.MatchString(phrase) {
			score += 3
		}
		if score > bestScore {
			best, bestScore = phrase, score
		}
	}
	return best
}

func importantWords(text string, result *scriptpkg.EntityResult, limit int, language string) []string {
	_ = language
	entityWords := map[string]struct{}{}
	for _, group := range [][]scriptpkg.Entity{result.Persons, result.Places} {
		for _, entity := range group {
			for _, word := range wordRE.FindAllString(strings.ToLower(entity.Value), -1) {
				entityWords[word] = struct{}{}
			}
		}
	}
	type token struct {
		text  string
		first int
	}
	tokens := make([]token, 0)
	for _, loc := range wordRE.FindAllStringIndex(text, -1) {
		tokens = append(tokens, token{text: text[loc[0]:loc[1]], first: len([]rune(text[:loc[0]]))})
	}
	type candidate struct {
		word                       string
		count, first, width, score int
	}
	counts := map[string]*candidate{}
	for width := 3; width >= 1; width-- {
		for i := 0; i+width <= len(tokens); i++ {
			parts := make([]string, 0, width)
			valid := true
			lengthScore := 0
			for j := 0; j < width; j++ {
				word := strings.ToLower(tokens[i+j].text)
				if len([]rune(word)) < 4 {
					valid = false
				}
				if _, entity := entityWords[word]; entity {
					valid = false
				}
				lengthScore += len([]rune(word))
				parts = append(parts, word)
			}
			if !valid {
				continue
			}
			phrase := strings.Join(parts, " ")
			if _, ok := counts[phrase]; !ok {
				counts[phrase] = &candidate{word: phrase, first: tokens[i].first, width: width, score: lengthScore + width*6}
			}
			counts[phrase].count++
		}
	}
	items := make([]candidate, 0, len(counts))
	for _, c := range counts {
		items = append(items, *c)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].score != items[j].score {
			return items[i].score > items[j].score
		}
		if items[i].count != items[j].count {
			return items[i].count > items[j].count
		}
		if items[i].width != items[j].width {
			return items[i].width > items[j].width
		}
		return items[i].first < items[j].first
	})
	if limit <= 0 || limit > 5 {
		limit = 5
	}
	if len(items) > limit {
		items = items[:limit]
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.word)
	}
	return out
}

var _ adapters.EntityExtractor = (*Extractor)(nil)
