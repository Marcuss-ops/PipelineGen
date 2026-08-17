// Package local provides deterministic, CPU-only semantic extraction.
package local

import (
	"context"
	"regexp"
	"sort"
	"strings"

	adapters "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/linguistics"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// Extractor is deliberately conservative: it only emits text found in the
// input and never calls a model or an external provider.
type Extractor struct{}

func NewExtractor() *Extractor { return &Extractor{} }

var (
	// properNameRE is retained for phrase ranking. Entity extraction itself
	// uses entitySpanRE below so internal organization connectors are kept in
	// the same candidate span.
	properNameRE = regexp.MustCompile(`(?:\b[\p{Lu}][\p{L}'’-]+)(?:\s+[\p{Lu}][\p{L}'’-]+)+`)
	entitySpanRE = regexp.MustCompile(`\b(?:[A-Z]{2,10}|[\p{Lu}][\p{L}'’-]+)(?:\s+(?:(?:of|and|the|for|&)|[A-Z]{2,10}|[\p{Lu}][\p{L}'’-]+))*`)
	// singleNameRE matches one capitalized word; it is only used to detect
	// single-word known places ("London", "Roma", "Parigi", "Londra") that
	// properNameRE (2+ capitalized words) cannot capture.
	singleNameRE = regexp.MustCompile(`\b[\p{Lu}][\p{L}'’-]+\b`)
	acronymRE    = regexp.MustCompile(`\b[A-Z]{2,8}\b`)
	yearRE       = regexp.MustCompile(`\b(?:1[89]\d{2}|20\d{2})\b`)
	wordRE       = regexp.MustCompile(`[\p{L}\p{M}]+(?:['’][\p{L}\p{M}]+)?`)
	sentenceRE   = regexp.MustCompile(`[^.!?]+(?:[.!?]+|$)`)
)

var knownPlaces = map[string]struct{}{
	"las vegas": {}, "stati uniti": {}, "united states": {}, "new york": {}, "roma": {}, "parigi": {}, "londra": {}, "london": {}, "los angeles": {},
}

var knownConcepts = map[string]struct{}{
	"genesis mission": {},
}

var titleCaseVerbs = map[string]struct{}{
	"accelerates": {}, "accelerate": {}, "appears": {}, "described": {}, "discussed": {},
	"joins": {}, "presented": {}, "presents": {}, "said": {}, "spoke": {},
	"supports": {}, "supported": {}, "works": {},
}

// nonEntityLeadWords are the tokens that must never begin a candidate entity
// (sentence-initial connectives/determiners that are not names). Kept as a
// slice because these are entity-extraction lead blockers, not a stop-word
// lexicon — the canonical stop-word SSOT lives in linguistics.
var nonEntityLeadWords = []string{
	"among", "at", "central", "from", "furthermore", "in", "the", "this",
}

func (e *Extractor) ExtractEntities(_ context.Context, req scriptpkg.EntityExtractionRequest) (*scriptpkg.EntityResult, error) {
	text := strings.TrimSpace(req.Text)
	result := &scriptpkg.EntityResult{Persons: []scriptpkg.Entity{}, Places: []scriptpkg.Entity{}, Concepts: []scriptpkg.Entity{}, ImportantPhrases: []string{}, ImportantWords: []string{}, ArtlistPhrases: []string{}}
	if text == "" {
		return result, nil
	}
	seen := map[string]struct{}{}
	properSpans := entitySpans(text)
	for _, span := range properSpans {
		value := normalizeEntityValue(trimEntityPrefix(text[span[0]:span[1]]))
		if isNonEntityLead(value) {
			continue
		}
		kind := classifyName(value)
		appendEntity(result, seen, kind, value, 0.90)
	}
	for _, match := range yearRE.FindAllString(text, -1) {
		appendEntity(result, seen, "DATE", match, 0.99)
	}
	// Single-word known places are emitted conservatively: only capitalized
	// words present in the knownPlaces lexicon and not already covered by a
	// multi-word proper name (so "Los" / "Angeles" are never double-counted
	// after "Los Angeles").
	for _, span := range singleNameRE.FindAllStringIndex(text, -1) {
		if spanInside(span, properSpans) {
			continue
		}
		word := strings.TrimSpace(text[span[0]:span[1]])
		if _, ok := knownPlaces[strings.ToLower(word)]; !ok {
			continue
		}
		appendEntity(result, seen, "GPE", word, 0.90)
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
	value = normalizeEntityValue(value)
	lower := strings.ToLower(strings.TrimSpace(trimEntityPrefix(value)))
	if _, ok := knownPlaces[lower]; ok {
		return "GPE"
	}
	if _, ok := knownConcepts[lower]; ok {
		return "CONCEPT"
	}
	if strings.EqualFold(strings.TrimSpace(value), "Earth") {
		return "CONCEPT"
	}
	if strings.Contains(lower, "white house") || strings.Contains(lower, " office ") ||
		strings.Contains(lower, " agency") || strings.Contains(lower, " administration") ||
		strings.Contains(lower, " department") || strings.Contains(lower, " institute") ||
		strings.Contains(lower, " university") || strings.HasSuffix(lower, " corporation") ||
		strings.HasSuffix(lower, " company") {
		return "ORG"
	}
	if acronymRE.MatchString(strings.TrimSpace(value)) && !strings.Contains(value, " ") {
		return "ORG"
	}
	if strings.Contains(lower, " council") || strings.Contains(lower, " federation") || strings.Contains(lower, " association") {
		return "ORG"
	}
	return "PERSON"
}

func normalizeEntityValue(value string) string {
	return strings.Trim(strings.TrimSpace(value), ".,;:!?\"'’”)]}>")
}

func isNonEntityLead(value string) bool {
	parts := strings.Fields(strings.ToLower(normalizeEntityValue(value)))
	if len(parts) == 0 {
		return true
	}
	for _, blocked := range nonEntityLeadWords {
		if blocked == parts[0] {
			return true
		}
	}
	return false
}

// entitySpans resolves candidate names before classification. It keeps
// capitalized runs and organization connectors together, splits title-case
// verbs that commonly start the predicate after an acronym/name, and applies
// longest-span-wins so contained tokens cannot become separate entities.
func entitySpans(text string) [][]int {
	raw := entitySpanRE.FindAllStringIndex(text, -1)
	spans := make([][]int, 0, len(raw))
	for _, span := range raw {
		value := strings.TrimSpace(text[span[0]:span[1]])
		if value == "" {
			continue
		}
		parts := strings.Fields(value)
		// An acronym at the beginning is a complete candidate even when the
		// following title-case words continue in the same headline. This keeps
		// "NASA and the Genesis Mission" as NASA + Genesis Mission.
		if len(parts) > 1 && acronymRE.MatchString(parts[0]) {
			acronym := strings.TrimSpace(parts[0])
			if offset := strings.Index(text[span[0]:span[1]], acronym); offset >= 0 {
				spans = append(spans, []int{span[0] + offset, span[0] + offset + len(acronym)})
			}
			start := 1
			for start < len(parts) {
				lower := strings.ToLower(strings.Trim(parts[start], ".,;:!?()[]{}\"'"))
				if lower != "of" && lower != "and" && lower != "the" && lower != "for" && lower != "&" {
					break
				}
				start++
			}
			if start >= len(parts) {
				continue
			}
			value = strings.Join(parts[start:], " ")
			parts = parts[start:]
			spanStart := strings.Index(text[span[0]:span[1]], value)
			if spanStart >= 0 {
				span = []int{span[0] + spanStart, span[0] + spanStart + len(value)}
			}
		}
		start := 0
		for i, part := range parts {
			lower := strings.ToLower(strings.Trim(part, ".,;:!?()[]{}\"'"))
			if i > 0 {
				if _, verb := titleCaseVerbs[lower]; verb {
					// Title-case verbs are predicates, not entity names. Drop the
					// verb and start a fresh candidate after it.
					start = i + 1
				}
			}
		}
		candidate := strings.TrimSpace(strings.Join(parts[start:], " "))
		candidate = normalizeEntityValue(candidate)
		if len(parts) == 1 && !acronymRE.MatchString(parts[0]) {
			lower := strings.ToLower(strings.Trim(parts[0], ".,;:!?()[]{}\"'"))
			if _, place := knownPlaces[lower]; !place {
				if _, concept := knownConcepts[lower]; !concept && lower != "earth" {
					continue
				}
			}
		}
		if candidate != "" {
			if offset := strings.Index(text[span[0]:span[1]], candidate); offset >= 0 {
				spans = append(spans, []int{span[0] + offset, span[0] + offset + len(candidate)})
			}
		}
	}
	// Longest valid spans win. Stable source order is retained for equal
	// lengths, which keeps the result deterministic for callers and tests.
	sort.SliceStable(spans, func(i, j int) bool {
		li, lj := spans[i][1]-spans[i][0], spans[j][1]-spans[j][0]
		if li != lj {
			return li > lj
		}
		return spans[i][0] < spans[j][0]
	})
	selected := make([][]int, 0, len(spans))
	for _, span := range spans {
		contained := false
		for _, kept := range selected {
			if span[0] >= kept[0] && span[1] <= kept[1] {
				contained = true
				break
			}
		}
		if !contained {
			selected = append(selected, span)
		}
	}
	sort.SliceStable(selected, func(i, j int) bool { return selected[i][0] < selected[j][0] })
	return selected
}

// spanInside reports whether span lies fully within one of the provided
// spans, used to keep single-word place detection from re-emitting words that
// already belong to a multi-word proper name.
func spanInside(span []int, spans [][]int) bool {
	for _, s := range spans {
		if span[0] >= s[0] && span[1] <= s[1] {
			return true
		}
	}
	return false
}

func trimEntityPrefix(value string) string {
	parts := strings.Fields(strings.TrimSpace(value))
	for len(parts) > 0 {
		switch strings.ToLower(parts[0]) {
		case "a", "ad", "al", "alla", "da", "dal", "dalla", "di", "il", "la", "nel", "nella", "in", "the",
			"president", "prime", "minister", "senator", "governor", "director", "dr", "mr", "mrs", "ms":
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
		if isNonNarrativeInstruction(phrase) {
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
	if strings.TrimSpace(language) == "" {
		language = "fallback"
	}
	profile := linguistics.DefaultLexicon().Resolve(language)
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
		word                string
		count, first, score int
	}
	counts := map[string]*candidate{}
	for _, tok := range tokens {
		word := strings.ToLower(tok.text)
		if len([]rune(word)) < 4 {
			continue
		}
		if _, entity := entityWords[word]; entity {
			continue
		}
		if _, stop := profile.StopWords[word]; stop {
			continue
		}
		if _, function := profile.FunctionWords[word]; function {
			continue
		}
		item := counts[word]
		if item == nil {
			item = &candidate{word: word, first: tok.first}
			counts[word] = item
		}
		item.count++
		item.score = len([]rune(word))*2 + item.count*3
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

func isNonNarrativeInstruction(sentence string) bool {
	lower := strings.ToLower(strings.TrimSpace(sentence))
	markers := []string{
		"search should", "should focus on", "a realistic editorial photograph",
		"the relevant visual subjects", "the central subject is", "useful supporting details",
		"these concrete details should", "these specific terms distinguish",
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

var _ adapters.EntityExtractor = (*Extractor)(nil)
