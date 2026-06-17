// Package queryrouter classifies search queries by modality and returns
// adaptive RRF (Reciprocal Rank Fusion) weights for Qdrant hybrid search.
//
// Rules (deterministic, no LLM):
//   - Quoted text → "quote" (search transcript + BM25 primarily)
//   - Proper names (capitalized, multi-word) → "person" (BM25 + text)
//   - Visual words (colors, actions, scene descriptors) → "visual" (text + visual)
//   - Short noun phrases → "lexical" (BM25-heavy)
//   - Abstract/long queries → "semantic" (text-heavy)
//   - Default → "mixed" (balanced)
package queryrouter

import (
	"strings"
	"unicode"
)

// SearchProfile defines the modality weights for hybrid search.
// CandidateLimitMultiplier adjusts how many prefetch candidates Qdrant fetches.
// MinScoreAdjust shifts the minimum score threshold (negative = more permissive).
type SearchProfile struct {
	Name                     string
	TextWeight               float64
	TranscriptWeight         float64
	BM25Weight               float64
	VisualWeight             float64
	CandidateLimitMultiplier float64
	MinScoreAdjust           float64
}

var (
	ProfileSemantic = SearchProfile{
		Name: "semantic", TextWeight: 1.0, TranscriptWeight: 0.5, BM25Weight: 0.3,
		VisualWeight: 0.0, CandidateLimitMultiplier: 1.0, MinScoreAdjust: 0.0,
	}
	ProfileQuote = SearchProfile{
		Name: "quote", TextWeight: 0.3, TranscriptWeight: 1.0, BM25Weight: 1.0,
		VisualWeight: 0.0, CandidateLimitMultiplier: 1.5, MinScoreAdjust: -0.05,
	}
	ProfileVisual = SearchProfile{
		Name: "visual", TextWeight: 1.0, TranscriptWeight: 0.0, BM25Weight: 0.2,
		VisualWeight: 1.0, CandidateLimitMultiplier: 1.5, MinScoreAdjust: -0.05,
	}
	ProfilePerson = SearchProfile{
		Name: "person", TextWeight: 0.6, TranscriptWeight: 0.3, BM25Weight: 1.0,
		VisualWeight: 0.0, CandidateLimitMultiplier: 1.2, MinScoreAdjust: 0.0,
	}
	ProfileMixed = SearchProfile{
		Name: "mixed", TextWeight: 1.0, TranscriptWeight: 0.7, BM25Weight: 0.5,
		VisualWeight: 0.0, CandidateLimitMultiplier: 1.0, MinScoreAdjust: 0.0,
	}
)

var visualWords = map[string]bool{
	"red": true, "blue": true, "green": true, "yellow": true, "black": true, "white": true,
	"dark": true, "bright": true, "colorful": true, "neon": true, "golden": true, "silver": true,
	"running": true, "walking": true, "driving": true, "flying": true, "swimming": true, "dancing": true,
	"wearing": true, "holding": true, "carrying": true, "sitting": true, "standing": true, "lying": true,
	"city": true, "street": true, "building": true, "room": true, "office": true, "kitchen": true,
	"desert": true, "mountain": true, "ocean": true, "beach": true, "forest": true, "river": true,
	"night": true, "sunset": true, "sunrise": true, "daytime": true, "indoor": true, "outdoor": true,
	"car": true, "bus": true, "train": true, "plane": true, "boat": true, "bicycle": true,
	"close-up": true, "wide": true, "panoramic": true, "aerial": true, "slow-motion": true,
	"crowd": true, "empty": true, "busy": true, "quiet": true,
	"scene": true, "shot": true, "footage": true, "clip": true, "background": true, "landscape": true,
	"view": true, "image": true, "picture": true, "photo": true, "visual": true, "looking": true,
	"showing": true, "displaying": true, "appears": true, "seen": true,
}

func hasProperName(query string) bool {
	words := strings.Fields(query)
	capCount := 0
	for _, w := range words {
		if len(w) > 1 && unicode.IsUpper(rune(w[0])) {
			capCount++
		}
	}
	return capCount > 0 && float64(capCount)/float64(len(words)) > 0.4
}

func hasQuotes(query string) bool {
	return strings.Count(query, `"`) >= 2 || strings.Count(query, `'`) >= 2
}

func isVisualQuery(query string) bool {
	words := strings.Fields(strings.ToLower(query))
	visualCount := 0
	for _, w := range words {
		w = strings.Trim(w, ".,!?;:'\"()[]{}")
		if visualWords[w] {
			visualCount++
		}
	}
	return visualCount >= 2
}

func isShortNounPhrase(query string) bool {
	words := strings.Fields(query)
	if len(words) > 4 {
		return false
	}
	verbWords := map[string]bool{
		"is": true, "are": true, "was": true, "were": true, "be": true,
		"have": true, "has": true, "had": true, "do": true, "does": true,
		"will": true, "would": true, "can": true, "could": true, "should": true,
		"make": true, "made": true, "get": true, "got": true, "go": true,
		"say": true, "said": true, "tell": true, "told": true, "talk": true,
		"show": true, "shows": true, "find": true, "look": true, "see": true,
		"want": true, "need": true, "like": true, "use": true, "know": true,
	}
	for _, w := range words {
		if verbWords[strings.ToLower(w)] {
			return false
		}
	}
	return true
}

// Classify determines the best search profile for a query using deterministic rules.
// Returns the profile and a confidence score (0.0-1.0).
func Classify(query string) (SearchProfile, float64) {
	query = strings.TrimSpace(query)
	if query == "" {
		return ProfileMixed, 1.0
	}
	if hasQuotes(query) {
		return ProfileQuote, 0.9
	}
	if isVisualQuery(query) {
		return ProfileVisual, 0.85
	}
	if hasProperName(query) && isShortNounPhrase(query) {
		return ProfilePerson, 0.8
	}
	if isShortNounPhrase(query) && len(strings.Fields(query)) <= 3 {
		return ProfilePerson, 0.7
	}
	if len(strings.Fields(query)) > 8 {
		return ProfileSemantic, 0.75
	}
	return ProfileMixed, 0.6
}
