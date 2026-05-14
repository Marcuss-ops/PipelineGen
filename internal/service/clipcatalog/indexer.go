package clipcatalog

import (
	"strings"
	"unicode"

	"velox/go-master/pkg/matchingconfig"
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

// ComputeQualityScore computes a quality score for a clip
// Weights are loaded from config/matching.yaml
func ComputeQualityScore(clip ClipMetadata, cfg *matchingconfig.MatchingConfig) float64 {
	score := cfg.ClipQuality.BaseScore

	// Boost if has embedding
	if len(clip.Embedding) > 0 {
		score += cfg.ClipQuality.EmbeddingBonus
	}

	// Boost if has category
	if clip.Category != "" {
		score += cfg.ClipQuality.CategoryBonus
	}

	// Penalty for high reuse
	if clip.ReuseCount > cfg.ClipQuality.ReuseThreshold {
		score -= cfg.ClipQuality.ReusePenaltyPerUse * float64(clip.ReuseCount-cfg.ClipQuality.ReuseThreshold)
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
func BuildClipMetadataFromModel(clip *models.MediaAsset, cfg *matchingconfig.MatchingConfig) ClipMetadata {
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
	meta.QualityScore = ComputeQualityScore(meta, cfg)

	return meta
}
