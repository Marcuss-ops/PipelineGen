// Package scriptdocs orchestrates script generation + entity extraction + clip association + Google Docs upload.
package scriptdocs

import (
	"strings"
	"unicode"

	"velox/go-master/internal/entityimages"
	"velox/go-master/pkg/logger"
	"velox/go-master/pkg/util"

	"go.uber.org/zap"
)

// Global image finder instance
var imageFinder = entityimages.New()

// ExtractSentences splits text into meaningful sentences (minimum 40 characters).
func ExtractSentences(text string) []string {
	raw := strings.Split(text, ".")
	var result []string
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if len(s) > 40 {
			result = append(result, s)
		}
	}
	return result
}

// ExtractProperNouns finds capitalized words that are too short to be noise.
func ExtractProperNouns(sentences []string) []string {
	return extractProperNounsInternal(sentences)
}

// extractProperNouns finds capitalized words that are too short to be noise.
func extractProperNouns(sentences []string) []string {
	return extractProperNounsInternal(sentences)
}

// ExtractKeywords extracts keywords from text.
func ExtractKeywords(text string) []string {
	return extractKeywords(text)
}

// extractProperNouns finds capitalized words that are too short to be noise.
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
		"del": true, "della": true, "dello": true, "dei": true, "delle": true,
		"al": true, "alla": true, "allo": true, "ai": true, "alle": true,
	}

	for _, sentence := range sentences {
		for _, word := range strings.Fields(sentence) {
			// Clean word: strip punctuation
			clean := strings.TrimFunc(word, func(r rune) bool {
				return !unicode.IsLetter(r) && !unicode.IsDigit(r)
			})
			if len(clean) <= 2 {
				continue
			}
			// Check if starts with uppercase
			if !unicode.IsUpper(rune(clean[0])) {
				continue
			}
			// Skip common words that happen to be capitalized
			lower := strings.ToLower(clean)
			if stopWords[lower] {
				continue
			}
			if !seen[clean] {
				seen[clean] = true
				result = append(result, clean)
			}
			if len(result) >= 4 {
				return result
			}
		}
	}

	return result
}

// ExtractEntitiesWithImages finds proper nouns and searches for actual image URLs.
func ExtractEntitiesWithImages(sentences []string) map[string]string {
	return extractEntitiesWithImagesInternal(sentences)
}

// extractEntitiesWithImages finds proper nouns and searches for actual image URLs.
func extractEntitiesWithImages(sentences []string) map[string]string {
	return extractEntitiesWithImagesInternal(sentences)
}

// extractEntitiesWithImagesInternal finds proper nouns and searches for actual image URLs.
func extractEntitiesWithImagesInternal(sentences []string) map[string]string {
	nouns := ExtractProperNouns(sentences)
	multiWord := ExtractMultiWordEntities(sentences)
	result := make(map[string]string)

	// Filter to image-worthy entities (people, places, organizations)
	skip := map[string]bool{
		"his": true, "her": true, "its": true, "the": true,
		"new": true, "old": true, "first": true, "last": true,
		"born": true, "year": true, "month": true, "day": true,
	}

	// Build a set of single words that appear inside multi-word entities
	// to avoid showing both "Gervonta Davis" AND "Gervonta" AND "Davis"
	containedInMultiWord := make(map[string]bool)
	for _, mw := range multiWord {
		parts := strings.Fields(mw)
		for _, part := range parts {
			clean := strings.TrimFunc(part, func(r rune) bool {
				return !unicode.IsLetter(r) && !unicode.IsDigit(r)
			})
			containedInMultiWord[strings.ToLower(clean)] = true
		}
	}

	// Process multi-word entities first (higher priority)
	for _, entity := range multiWord {
		lower := strings.ToLower(entity)
		if skip[lower] || len(entity) < 5 {
			continue
		}
		imageURL := imageFinder.Find(entity)
		if imageURL != "" {
			result[entity] = imageURL
		}
	}

	// Then process single nouns — skip if contained in a multi-word entity
	for _, noun := range nouns {
		lower := strings.ToLower(noun)
		if skip[lower] || len(noun) < 4 {
			continue
		}
		// Skip if already captured as part of multi-word
		if containedInMultiWord[lower] {
			continue
		}
		// Skip if exact match already exists
		if result[noun] != "" {
			continue
		}
		imageURL := imageFinder.Find(noun)
		if imageURL != "" {
			result[noun] = imageURL
		}
	}

	logger.Info("Entity image extraction completed",
		zap.Int("entities", len(result)),
	)

	return result
}

// ExtractMultiWordEntities finds consecutive capitalized words.
func ExtractMultiWordEntities(sentences []string) []string {
	return extractMultiWordEntities(sentences)
}

// extractMultiWordEntities finds consecutive capitalized words.
func extractMultiWordEntities(sentences []string) []string {
	seen := make(map[string]bool)
	var result []string

	for _, sentence := range sentences {
		words := strings.Fields(sentence)
		var currentGroup []string

		for _, word := range words {
			clean := strings.TrimFunc(word, func(r rune) bool {
				return !unicode.IsLetter(r) && !unicode.IsDigit(r)
			})
			if len(clean) <= 1 {
				// End current group
				if len(currentGroup) >= 2 {
					phrase := strings.Join(currentGroup, " ")
					if !seen[phrase] {
						seen[phrase] = true
						result = append(result, phrase)
					}
				}
				currentGroup = nil
				continue
			}
			if unicode.IsUpper(rune(clean[0])) {
				currentGroup = append(currentGroup, clean)
			} else {
				// End current group
				if len(currentGroup) >= 2 {
					phrase := strings.Join(currentGroup, " ")
					if !seen[phrase] {
						seen[phrase] = true
						result = append(result, phrase)
					}
				}
				currentGroup = nil
			}
		}
		// Don't forget trailing group
		if len(currentGroup) >= 2 {
			phrase := strings.Join(currentGroup, " ")
			if !seen[phrase] {
				seen[phrase] = true
				result = append(result, phrase)
			}
		}
	}

	return result
}

// extractKeywords performs frequency-based keyword extraction.
func extractKeywords(text string) []string {
	wordCount := make(map[string]int)
	totalWords := 0

	for _, word := range strings.Fields(strings.ToLower(text)) {
		clean := strings.TrimFunc(word, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		})
		if len(clean) <= 4 {
			continue
		}
		wordCount[clean]++
		totalWords++
	}

	// Sort by frequency — simple approach: pick top N
	type kv struct {
		Key   string
		Value int
	}
	var sorted []kv
	for k, v := range wordCount {
		sorted = append(sorted, kv{k, v})
	}
	// Bubble sort by value descending (small N, acceptable)
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].Value > sorted[i].Value {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	logger.Debug("Keyword extraction completed",
		zap.Int("total_words", totalWords),
		zap.Int("unique_keywords", len(sorted)),
	)

	result := make([]string, 0, util.Min(4, len(sorted)))
	for i := 0; i < len(sorted) && i < 4; i++ {
		result = append(result, sorted[i].Key)
	}
	return result
}
