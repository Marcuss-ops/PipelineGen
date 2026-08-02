package dto

import (
	"regexp"
	"sort"
	"strings"
)

// ExtractPeopleTags extracts likely person names from text segments using
// capitalized-phrase heuristics.
func ExtractPeopleTags(parts ...string) []string {
	out := make([]string, 0)
	seen := make(map[string]struct{})
	for _, text := range parts {
		for _, phrase := range ExtractCapitalizedPhrases(text) {
			norm := NormalizeClipTag(phrase)
			if norm == "" || IsGenericClipTag(norm) || IsGenericPersonPhrase(norm) {
				continue
			}
			if _, ok := seen[norm]; ok {
				continue
			}
			seen[norm] = struct{}{}
			out = append(out, norm)
		}
	}
	return out
}

// ExtractCapitalizedPhrases matches proper-noun phrases (2-3 capitalized words).
func ExtractCapitalizedPhrases(text string) []string {
	if text == "" {
		return nil
	}
	re := regexp.MustCompile(`\b[A-Z][a-z]+(?:\s+[A-Z][a-z]+){1,2}\b`)
	matches := re.FindAllString(text, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, m := range matches {
		norm := NormalizeClipTag(m)
		if norm == "" || IsGenericClipTag(norm) {
			continue
		}
		if _, ok := seen[norm]; ok {
			continue
		}
		seen[norm] = struct{}{}
		out = append(out, m)
	}
	return out
}

// ExtractTopicTags extracts topic-like phrases from text.
func ExtractTopicTags(text string) []string {
	phrases := ExtractConceptTags(text, 6)
	return NormalizeClipTagList(phrases)
}

// ExtractConceptTags extracts up to max frequent, non-stopword terms.
func ExtractConceptTags(text string, max int) []string {
	text = strings.TrimSpace(text)
	if text == "" || max <= 0 {
		return nil
	}
	freq := make(map[string]int)
	order := make([]string, 0)
	wordRe := regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9']+`)
	for _, raw := range strings.FieldsFunc(text, func(r rune) bool {
		return r == '\n' || r == '\t' || r == '.' || r == ',' || r == ';' || r == ':' || r == '!' || r == '?' || r == '/' || r == '|' || r == '(' || r == ')'
	}) {
		words := wordRe.FindAllString(strings.ToLower(raw), -1)
		for _, w := range words {
			if len(w) < 6 {
				continue
			}
			if IsGenericToken(w) || IsGenericClipTag(w) {
				continue
			}
			if _, ok := freq[w]; !ok {
				order = append(order, w)
			}
			freq[w]++
		}
	}
	type kv struct {
		word  string
		score int
	}
	ranked := make([]kv, 0, len(freq))
	for _, w := range order {
		ranked = append(ranked, kv{word: w, score: freq[w]})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			return len(ranked[i].word) > len(ranked[j].word)
		}
		return ranked[i].score > ranked[j].score
	})
	out := make([]string, 0, max)
	seen := make(map[string]struct{})
	for _, item := range ranked {
		if _, ok := seen[item.word]; ok {
			continue
		}
		seen[item.word] = struct{}{}
		out = append(out, item.word)
		if len(out) >= max {
			break
		}
	}
	return out
}
