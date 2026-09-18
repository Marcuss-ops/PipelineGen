package scriptgeneration

import (
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
)

// importantPhraseWord matches source words while retaining their byte spans.
// Keeping the spans lets the selector return the exact original surface,
// including accents, apostrophes, and punctuation between words.
var importantPhraseWord = regexp.MustCompile(`[\p{L}\p{M}]+(?:['’][\p{L}\p{M}]+)?`)

type importantPhraseToken struct {
	text       string
	start, end int
	group      int
}

type importantPhraseCandidate struct {
	text       string
	start      int
	tokenStart int
	tokenEnd   int
	score      int
}

// deterministicImportantPhrases returns short, source-grounded fragments in
// editorial-strength order. It uses only configured stop/function words and
// visual verbs; it makes no model or network calls and never rewrites text.
func deterministicImportantPhrases(text string, entities []VisualEntity, limit int, language string) []string {
	profile := importantPhraseLexicon(language)
	return selectImportantPhrases(text, entities, limit, profile)
}

func selectImportantPhrases(text string, entities []VisualEntity, limit int, profile *linguistics.LexiconProfile) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	policy := linguistics.DefaultPhraseExtractionPolicy()
	if profile != nil {
		policy = profile.PhrasePolicy
	}
	if policy.MinWords < 2 {
		policy.MinWords = 2
	}
	if policy.MaxWords < policy.MinWords {
		policy.MaxWords = policy.MinWords
	}
	// Phrase overlays are short editorial headlines. Keep the selection within
	// four words even if a future lexicon policy allows longer retrieval phrases.
	if policy.MaxWords > 4 {
		policy.MaxWords = 4
	}
	if limit <= 0 {
		limit = policy.MaxResults
	}
	if limit <= 0 {
		limit = 3
	}
	if policy.MaxResults > 0 && limit > policy.MaxResults {
		limit = policy.MaxResults
	}

	entitySpans := make([][2]int, 0, len(entities))
	for _, entity := range entities {
		span, ok := findEntitySpan(text, entity.Text)
		if ok {
			entitySpans = append(entitySpans, [2]int{span.StartRune, span.EndRune})
		}
	}

	tokens := tokenizeImportantPhrases(text)
	if len(tokens) < policy.MinWords {
		return nil
	}
	candidates := make([]importantPhraseCandidate, 0, len(tokens))
	for start := range tokens {
		for wordCount := policy.MinWords; wordCount <= policy.MaxWords && start+wordCount <= len(tokens); wordCount++ {
			end := start + wordCount - 1
			if tokens[start].group != tokens[end].group {
				break
			}
			first, last := strings.ToLower(tokens[start].text), strings.ToLower(tokens[end].text)
			if isImportantPhraseFunctionWord(first, profile) || isImportantPhraseFunctionWord(last, profile) {
				continue
			}
			contentWords, visualVerbs := 0, 0
			allVisualVerbs := true
			for i := start; i <= end; i++ {
				word := strings.ToLower(tokens[i].text)
				if isImportantPhraseFunctionWord(word, profile) || len([]rune(word)) < 3 {
					continue
				}
				contentWords++
				if isConfiguredVisualVerb(word, profile) {
					visualVerbs++
				} else {
					allVisualVerbs = false
				}
			}
			if contentWords < 2 || (policy.RejectVerbsWhenAll && allVisualVerbs) {
				continue
			}

			byteStart, byteEnd := tokens[start].start, tokens[end].end
			candidateText := text[byteStart:byteEnd]
			if containsProperNamePair(candidateText) {
				continue
			}
			runeStart := utf8.RuneCountInString(text[:byteStart])
			runeEnd := runeStart + utf8.RuneCountInString(candidateText)
			if overlapsAnyRuneSpan(runeStart, runeEnd, entitySpans) {
				continue
			}

			// More grounded content words and configured action verbs increase
			// salience. The shorter-length term breaks otherwise equal choices
			// toward compact overlays; source order breaks the final tie.
			score := contentWords*4 + visualVerbs*5 - wordCount
			candidates = append(candidates, importantPhraseCandidate{
				text: candidateText, start: byteStart,
				tokenStart: start, tokenEnd: end, score: score,
			})
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		if candidates[i].start != candidates[j].start {
			return candidates[i].start < candidates[j].start
		}
		return candidates[i].tokenEnd < candidates[j].tokenEnd
	})

	selected := make([]importantPhraseCandidate, 0, limit)
	seen := make(map[string]struct{}, limit)
	for _, candidate := range candidates {
		key := strings.ToLower(strings.Join(strings.Fields(candidate.text), " "))
		if _, exists := seen[key]; exists {
			continue
		}
		overlaps := false
		for _, prior := range selected {
			if candidate.tokenStart <= prior.tokenEnd && prior.tokenStart <= candidate.tokenEnd {
				overlaps = true
				break
			}
		}
		if overlaps {
			continue
		}
		seen[key] = struct{}{}
		selected = append(selected, candidate)
		if len(selected) == limit {
			break
		}
	}

	phrases := make([]string, len(selected))
	for i, candidate := range selected {
		phrases[i] = candidate.text
	}
	return phrases
}

func deterministicImportantWords(phrases []string, limit int, language string) []string {
	return deterministicImportantWordsWithProfile(phrases, limit, importantPhraseLexicon(language))
}

func deterministicImportantWordsWithProfile(phrases []string, limit int, profile *linguistics.LexiconProfile) []string {
	if limit <= 0 {
		return nil
	}
	words := make([]string, 0, limit)
	seen := make(map[string]struct{}, limit)
	for _, phrase := range phrases {
		for _, match := range importantPhraseWord.FindAllString(phrase, -1) {
			key := strings.ToLower(match)
			if len([]rune(key)) < 3 || isImportantPhraseFunctionWord(key, profile) {
				continue
			}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			words = append(words, match)
			if len(words) == limit {
				return words
			}
		}
	}
	return words
}

func importantPhraseLexicon(language string) *linguistics.LexiconProfile {
	registry := linguistics.DefaultLexiconOrNil()
	if registry == nil {
		return nil
	}
	if registry.HasProfile(language) {
		return registry.Resolve(language)
	}
	if registry.HasProfile("fallback") {
		return registry.Resolve("fallback")
	}
	return nil
}

func tokenizeImportantPhrases(text string) []importantPhraseToken {
	indexes := importantPhraseWord.FindAllStringIndex(text, -1)
	tokens := make([]importantPhraseToken, 0, len(indexes))
	group := 0
	for i, index := range indexes {
		if i > 0 && phraseBoundaryBetween(text[indexes[i-1][1]:index[0]]) {
			group++
		}
		tokens = append(tokens, importantPhraseToken{
			text: text[index[0]:index[1]], start: index[0], end: index[1], group: group,
		})
	}
	return tokens
}

func phraseBoundaryBetween(gap string) bool {
	return strings.ContainsAny(gap, ".!?;,:—–\n\r")
}

func isImportantPhraseFunctionWord(word string, profile *linguistics.LexiconProfile) bool {
	if profile == nil {
		return false
	}
	_, stop := profile.StopWords[word]
	_, function := profile.FunctionWords[word]
	return stop || function
}

func isConfiguredVisualVerb(word string, profile *linguistics.LexiconProfile) bool {
	if profile == nil {
		return false
	}
	_, ok := profile.VisualVerbs[word]
	return ok
}

func overlapsAnyRuneSpan(start, end int, spans [][2]int) bool {
	for _, span := range spans {
		if start < span[1] && span[0] < end {
			return true
		}
	}
	return false
}
