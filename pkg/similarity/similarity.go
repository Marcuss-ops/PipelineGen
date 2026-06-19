// Package similarity provides generic token set and similarity utilities.
package similarity

import "strings"

// TokenSet splits text into a set of lowercase tokens, filtering words shorter than 3 characters.
// Non-alphanumeric characters are replaced with spaces before splitting.
func TokenSet(text string) map[string]struct{} {
	text = strings.ToLower(text)
	text = strings.NewReplacer(
		",", " ", ".", " ", "!", " ", "?", " ", ";", " ", ":", " ",
		"(", " ", ")", " ", "[", " ", "]", " ", "-", " ", "_", " ",
		"\"", " ", "'", " ", "/", " ", "\\", " ",
		"&", " ", "|", " ", "#", " ",
	).Replace(text)
	set := make(map[string]struct{})
	for _, word := range strings.Fields(text) {
		if len(word) < 3 {
			continue
		}
		set[word] = struct{}{}
	}
	return set
}

// TokenSetFromTokens merges multiple string slices into a single token set.
// Each string is tokenized via TokenSet.
func TokenSetFromTokens(values ...[]string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, list := range values {
		for _, item := range list {
			for tok := range TokenSet(item) {
				set[tok] = struct{}{}
			}
		}
	}
	return set
}

// Jaccard computes the Jaccard similarity coefficient between two token sets.
// Returns 0 if either set is empty.
func Jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	intersection := 0
	for k := range a {
		if _, ok := b[k]; ok {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union <= 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// OverlapRatio computes the overlap ratio between two time intervals.
// Returns 0 if either interval is invalid or has zero duration.
func OverlapRatio(aStart, aEnd, bStart, bEnd int) float64 {
	if aEnd <= aStart || bEnd <= bStart {
		return 0
	}
	start := max(aStart, bStart)
	end := min(aEnd, bEnd)
	if end <= start {
		return 0
	}
	overlap := end - start
	shorter := min(aEnd-aStart, bEnd-bStart)
	if shorter <= 0 {
		return 0
	}
	return float64(overlap) / float64(shorter)
}
