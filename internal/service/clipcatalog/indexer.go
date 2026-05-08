package clipcatalog

import (
	"strings"
	"unicode"

	"velox/go-master/pkg/models"
	"velox/go-master/pkg/textutil"
)

// BuildSearchText generates search_text from clip metadata
func BuildSearchText(clip ClipMetadata) string {
	parts := make([]string, 0)

	// Add name tokens (normalized)
	if clip.Name != "" {
		parts = append(parts, textutil.Tokenize(clip.Name)...)
	}

	// Add any existing search text tokens
	if clip.SearchText != "" {
		parts = append(parts, textutil.Tokenize(clip.SearchText)...)
	}

	// Remove duplicates and join
	seen := make(map[string]bool)
	unique := make([]string, 0)
	for _, p := range parts {
		lower := strings.ToLower(p)
		if !seen[lower] && lower != "" && len(lower) > 2 {
			seen[lower] = true
			unique = append(unique, p)
		}
	}

	return strings.Join(unique, " ")
}

// InferCategoryFromSearchTerm infers category from search term
func InferCategoryFromSearchTerm(term string) string {
	termLower := strings.ToLower(term)

	categories := map[string][]string{
		"rural_life": {"amish", "farm", "countryside", "rural", "barn", "field", "horse", "buggy", "wagon"},
		"urban":       {"city", "urban", "street", "building", "downtown", "metropolis"},
		"nature":      {"forest", "mountain", "river", "lake", "tree", "landscape", "sunset", "sunrise"},
		"technology":  {"computer", "tech", "code", "programming", "digital", "screen"},
		"people":      {"person", "people", "family", "group", "crowd", "man", "woman"},
		"food":        {"food", "cooking", "restaurant", "meal", "dinner", "lunch", "pizza"},
		"sports":      {"sport", "game", "football", "basketball", "soccer", "tennis"},
		"travel":      {"travel", "beach", "vacation", "tourism", "hotel", "flight"},
	}

	for category, keywords := range categories {
		for _, keyword := range keywords {
			if strings.Contains(termLower, keyword) {
				return category
			}
		}
	}

	return "general"
}

// ComputeQualityScore computes a quality score for a clip
func ComputeQualityScore(clip ClipMetadata) float64 {
	score := 0.5 // base score

	// Boost if has embedding
	if len(clip.Embedding) > 0 {
		score += 0.2
	}

	// Boost if has category
	if clip.Category != "" {
		score += 0.15
	}

	// Penalty for high reuse
	if clip.ReuseCount > 5 {
		score -= 0.1 * float64(clip.ReuseCount-5)
	}

	// Ensure score is between 0 and 1
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}

	return score
}

// NormalizeText normalizes text for comparison
func NormalizeText(text string) string {
	// Tokenize and remove stop words
	tokens := textutil.TokenizeWithStopWords(text)

	// Filter tokens
	normalized := make([]string, 0)
	for _, tok := range tokens {
		if len(tok) >= 3 && unicode.IsLetter(rune(tok[0])) {
			normalized = append(normalized, strings.ToLower(tok))
		}
	}

	return strings.Join(normalized, " ")
}

// ContainsNegativeTerm checks if text contains any negative terms
func ContainsNegativeTerm(text string, negativeTerms []string) (bool, string) {
	textLower := strings.ToLower(text)
	for _, term := range negativeTerms {
		if strings.Contains(textLower, strings.ToLower(term)) {
			return true, term
		}
	}
	return false, ""
}

// ExtendSearchText extends existing search text with new terms
func ExtendSearchText(existing, newTerms string) string {
	existingTokens := textutil.Tokenize(existing)
	newTokens := textutil.Tokenize(newTerms)

	// Merge and deduplicate
	seen := make(map[string]bool)
	allTokens := make([]string, 0)

	for _, tok := range existingTokens {
		lower := strings.ToLower(tok)
		if !seen[lower] {
			seen[lower] = true
			allTokens = append(allTokens, tok)
		}
	}

	for _, tok := range newTokens {
		lower := strings.ToLower(tok)
		if !seen[lower] {
			seen[lower] = true
			allTokens = append(allTokens, tok)
		}
	}

	return strings.Join(allTokens, " ")
}

// BuildClipMetadataFromModel builds ClipMetadata from models.Clip
func BuildClipMetadataFromModel(clip *models.Clip) ClipMetadata {
	meta := ClipMetadata{
		ID:         clip.ID,
		Name:       clip.Name,
		Category:   clip.Category,
		SearchText: strings.Join(clip.SearchTerms, " "),
	}

	// Build search text if empty
	if meta.SearchText == "" {
		meta.SearchText = BuildSearchText(meta)
	}

	// Compute quality score
	meta.QualityScore = ComputeQualityScore(meta)

	return meta
}
