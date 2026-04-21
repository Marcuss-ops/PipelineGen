// Package scriptdocs orchestrates script generation + entity extraction + Google Docs upload.
package scriptdocs

import (
	"regexp"
	"sort"
	"strings"
	"unicode"

	"velox/go-master/internal/entityimages"
	"velox/go-master/pkg/logger"
	"velox/go-master/pkg/util"

	"go.uber.org/zap"
)

var imageFinder = entityimages.New()

var sentenceSplitRE = regexp.MustCompile(`[.!?\n]+`)

// ExtractSentences splits text into meaningful sentences.
func ExtractSentences(text string) []string {
	raw := sentenceSplitRE.Split(text, -1)
	var result []string
	for _, s := range raw {
		s = normalizeWhitespace(s)
		if len(s) >= 25 {
			result = append(result, s)
		}
	}
	return result
}

// ExtractFactUnits splits long narrative sentences into smaller searchable facts.
func ExtractFactUnits(text string) []string {
	sentences := ExtractSentences(text)
	var facts []string
	for _, sentence := range sentences {
		parts := splitSentenceIntoFacts(sentence)
		facts = append(facts, parts...)
	}
	return dedupeOrderedStrings(facts)
}

// SelectImportantPhrases ranks fact units rather than blindly taking the first sentences.
func SelectImportantPhrases(text, topic string, limit int) []string {
	facts := ExtractFactUnits(text)
	if len(facts) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = 4
	}

	topicTokens := significantTokens(topic)
	type scoredFact struct {
		Text  string
		Score int
	}
	var scored []scoredFact
	for _, fact := range facts {
		score := phraseRelevanceScore(fact, topicTokens)
		scored = append(scored, scoredFact{Text: fact, Score: score})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		return len(scored[i].Text) > len(scored[j].Text)
	})

	result := make([]string, 0, util.Min(limit, len(scored)))
	for i := 0; i < len(scored) && len(result) < limit; i++ {
		if len(scored[i].Text) < 20 {
			continue
		}
		result = append(result, scored[i].Text)
	}
	return result
}

func splitSentenceIntoFacts(sentence string) []string {
	replacer := strings.NewReplacer(";", ".", " — ", ".", " - ", ".")
	sentence = replacer.Replace(sentence)
	parts := strings.Split(sentence, ",")
	var facts []string
	for _, p := range parts {
		p = normalizeWhitespace(p)
		if len(p) >= 20 {
			facts = append(facts, p)
		}
	}
	if len(facts) == 0 && len(sentence) >= 20 {
		facts = append(facts, normalizeWhitespace(sentence))
	}
	return facts
}

func phraseRelevanceScore(phrase string, topicTokens []string) int {
	lower := strings.ToLower(phrase)
	score := 0
	for _, tok := range topicTokens {
		if strings.Contains(lower, tok) {
			score += 5
		}
	}
	for _, noun := range extractProperNouns([]string{phrase}) {
		if len(noun) > 2 {
			score += 2
		}
	}
	if strings.Contains(lower, "born") || strings.Contains(lower, "trainer") || strings.Contains(lower, "foster") || strings.Contains(lower, "boxing") {
		score += 2
	}
	if len(phrase) > 40 {
		score += 1
	}
	return score
}

func significantTokens(text string) []string {
	parts := strings.Fields(strings.ToLower(text))
	stop := map[string]bool{"the": true, "and": true, "with": true, "for": true, "this": true, "that": true, "from": true, "into": true, "about": true}
	var out []string
	seen := map[string]bool{}
	for _, p := range parts {
		p = strings.TrimFunc(p, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
		if len(p) < 3 || stop[p] || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

func normalizeWhitespace(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

func dedupeOrderedStrings(items []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, item := range items {
		item = normalizeWhitespace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

func ExtractProperNouns(sentences []string) []string { return extractProperNounsInternal(sentences) }
func extractProperNouns(sentences []string) []string { return extractProperNounsInternal(sentences) }
func ExtractKeywords(text string) []string { return extractKeywords(text) }

func extractProperNounsInternal(sentences []string) []string {
	seen := make(map[string]bool)
	var result []string
	stopWords := map[string]bool{
		"the": true, "a": true, "an": true, "is": true, "are": true,
		"his": true, "her": true, "its": true, "was": true, "were": true,
		"he": true, "she": true, "it": true, "they": true, "we": true,
		"this": true, "that": true, "these": true, "those": true,
		"il": true, "la": true, "lo": true, "gli": true, "le": true,
		"un": true, "una": true, "uno": true, "e": true, "o": true,
		"di": true, "da": true, "in": true, "con": true, "su": true,
		"per": true, "tra": true, "fra": true, "non": true, "che": true,
		"davis": true, "tank": true, "baltimore": true, // User specific high-frequency but generic in context
	}
	for _, sentence := range sentences {
		for _, word := range strings.Fields(sentence) {
			clean := strings.TrimFunc(word, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
			if len(clean) <= 2 || !unicode.IsUpper(rune(clean[0])) {
				continue
			}
			if stopWords[strings.ToLower(clean)] {
				continue
			}
			if !seen[clean] {
				seen[clean] = true
				result = append(result, clean)
			}
			if len(result) >= 30 { // Increased from 20 to 30
				return result
			}
		}
	}
	return result
}

func ExtractEntitiesWithImages(sentences []string) map[string]string { return extractEntitiesWithImagesInternal(sentences) }
func extractEntitiesWithImages(sentences []string) map[string]string { return extractEntitiesWithImagesInternal(sentences) }

func extractEntitiesWithImagesInternal(sentences []string) map[string]string {
	nouns := ExtractProperNouns(sentences)
	multiWord := ExtractMultiWordEntities(sentences)
	result := make(map[string]string)
	skip := map[string]bool{"his": true, "her": true, "its": true, "the": true, "new": true, "old": true, "first": true, "last": true, "born": true, "year": true, "month": true, "day": true}
	containedInMultiWord := make(map[string]bool)
	for _, mw := range multiWord {
		for _, part := range strings.Fields(mw) {
			clean := strings.TrimFunc(part, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
			containedInMultiWord[strings.ToLower(clean)] = true
		}
	}
	for _, entity := range multiWord {
		lower := strings.ToLower(entity)
		if skip[lower] || len(entity) < 5 {
			continue
		}
		if imageURL := imageFinder.Find(entity); imageURL != "" {
			result[entity] = imageURL
		}
	}
	for _, noun := range nouns {
		lower := strings.ToLower(noun)
		if skip[lower] || len(noun) < 4 || containedInMultiWord[lower] || result[noun] != "" {
			continue
		}
		if imageURL := imageFinder.Find(noun); imageURL != "" {
			result[noun] = imageURL
		}
	}
	logger.Info("Entity image extraction completed", zap.Int("entities", len(result)))
	return result
}

func ExtractMultiWordEntities(sentences []string) []string { return extractMultiWordEntities(sentences) }

func extractMultiWordEntities(sentences []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, sentence := range sentences {
		words := strings.Fields(sentence)
		var currentGroup []string
		for _, word := range words {
			clean := strings.TrimFunc(word, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
			if len(clean) <= 1 {
				if len(currentGroup) >= 2 {
					phrase := strings.Join(currentGroup, " ")
					if !seen[phrase] { seen[phrase] = true; result = append(result, phrase) }
				}
				currentGroup = nil
				continue
			}
			if unicode.IsUpper(rune(clean[0])) {
				currentGroup = append(currentGroup, clean)
			} else {
				if len(currentGroup) >= 2 {
					phrase := strings.Join(currentGroup, " ")
					if !seen[phrase] { seen[phrase] = true; result = append(result, phrase) }
				}
				currentGroup = nil
			}
		}
		if len(currentGroup) >= 2 {
			phrase := strings.Join(currentGroup, " ")
			if !seen[phrase] { seen[phrase] = true; result = append(result, phrase) }
		}
	}
	return result
}

func extractKeywords(text string) []string {
	wordCount := make(map[string]int)
	totalWords := 0
	stopWords := map[string]bool{
		"della": true, "delle": true, "degli": true, "dallo": true, "dalla": true,
		"nelle": true, "nello": true, "nella": true, "sulla": true, "sulle": true,
		"questo": true, "quello": true, "tutti": true, "tutte": true, "tutto": true,
	}
	for _, word := range strings.Fields(strings.ToLower(text)) {
		clean := strings.TrimFunc(word, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
		if len(clean) <= 4 || stopWords[clean] {
			continue
		}
		wordCount[clean]++
		totalWords++
	}
	type kv struct{ Key string; Value int }
	var sortedKV []kv
	for k, v := range wordCount { sortedKV = append(sortedKV, kv{k, v}) }
	sort.Slice(sortedKV, func(i, j int) bool { return sortedKV[j].Value < sortedKV[i].Value })
	
	limit := 8
	result := make([]string, 0, util.Min(limit, len(sortedKV)))
	for i := 0; i < len(sortedKV) && i < limit; i++ { result = append(result, sortedKV[i].Key) }
	return result
}
