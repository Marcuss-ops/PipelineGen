package script

import (
	"fmt"
	"math"
	"strings"
)

// stripCodeFence removes markdown code fences from text.
func stripCodeFence(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```") {
		if idx := strings.Index(text[3:], "```"); idx != -1 {
			text = text[3 : 3+idx]
		}
	}
	return strings.TrimSpace(text)
}

// extractJSONObject extracts a JSON object from text.
func extractJSONObject(text string) string {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start == -1 || end == -1 || end <= start {
		return ""
	}
	return strings.TrimSpace(text[start : end+1])
}

// roundSeconds rounds seconds to one decimal place.
func roundSeconds(seconds float64) float64 {
	return math.Round(seconds*10) / 10
}

// formatTimestamp formats a timestamp for display.
func formatTimestamp(start, end float64) string {
	return fmt.Sprintf("%.0f-%.0f", start, end)
}

// cleanTimelineSentence cleans a sentence for timeline processing.
func cleanTimelineSentence(text string) string {
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "-")
	text = strings.TrimPrefix(text, "–")
	text = strings.TrimPrefix(text, "—")
	return strings.TrimSpace(text)
}

// extractOpeningAndClosingSentence extracts only the first and last sentences from text.
func extractOpeningAndClosingSentence(text string) (string, string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", ""
	}
	return firstSentence(text), lastSentence(text)
}

// firstSentence returns the first 30 characters of the text.
func firstSentence(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) > 30 {
		return string(runes[:30]) + "..."
	}
	return text
}

// lastSentence returns the last 30 characters of the text.
func lastSentence(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) > 30 {
		return "..." + string(runes[len(runes)-30:])
	}
	return text
}

// uniqueStrings returns unique strings from a slice.
func uniqueStrings(input []string) []string {
	if len(input) == 0 {
		return []string{}
	}
	seen := make(map[string]struct{}, len(input))
	result := make([]string, 0, len(input))
	for _, s := range input {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			result = append(result, s)
		}
	}
	return result
}

// extractLikelyNames extracts likely names from text.
func extractLikelyNames(text string) []string {
	var names []string
	words := strings.Fields(text)
	for i := 0; i < len(words); i++ {
		word := strings.Trim(words[i], `"'“”‘’,.:;!?()[]{}<>`)
		if len(word) < 3 {
			continue
		}
		runes := []rune(word)
		if len(runes) == 0 || runes[0] < 'A' || runes[0] > 'Z' {
			continue
		}
		if strings.EqualFold(word, "The") || strings.EqualFold(word, "And") || strings.EqualFold(word, "But") {
			continue
		}
		names = append(names, word)
	}
	return uniqueStrings(names)
}

// buildMatches builds matches from clips and terms.
func buildMatches(clips []stockClipRef, terms []string, seg TimelineSegment) []stockClipRef {
	matches := make([]stockClipRef, 0, len(clips))
	for _, clip := range clips {
		if shouldIncludeClip(clip, terms, seg) {
			matches = append(matches, clip)
		}
	}
	return matches
}

// shouldIncludeClip checks if a clip should be included in matches.
func shouldIncludeClip(clip stockClipRef, terms []string, seg TimelineSegment) bool {
	for _, term := range terms {
		if strings.Contains(strings.ToLower(clip.DisplayName()), strings.ToLower(term)) {
			return true
		}
	}
	return false
}

// scoreMatches scores matches based on terms and segment.
func scoreMatches(matches []stockClipRef, terms []string, seg TimelineSegment) []scoredMatch {
	// Implementation depends on scoredMatch type definition
	// Placeholder for now
	return []scoredMatch{}
}
