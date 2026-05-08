package script

import (
	"sort"
	"strings"
)

// extractImportantWords returns top N frequent non-stop words
func extractImportantWords(narrative string, max int) []string {
	stopWords := map[string]struct{}{
		"the": {}, "a": {}, "an": {}, "and": {}, "or": {}, "but": {}, "in": {},
		"on": {}, "at": {}, "to": {}, "for": {}, "of": {}, "with": {}, "by": {},
		"is": {}, "are": {}, "was": {}, "were": {}, "be": {}, "been": {}, "being": {},
		"this": {}, "that": {}, "these": {}, "those": {}, "it": {}, "its": {},
	}
	words := strings.Fields(strings.ToLower(narrative))
	freq := make(map[string]int)
	for _, w := range words {
		w = strings.TrimRight(w, ",.!?;:\"'")
		if w == "" {
			continue
		}
		if _, ok := stopWords[w]; ok {
			continue
		}
		if len([]rune(w)) < 3 {
			continue
		}
		freq[w]++
	}
	type wordFreq struct {
		word  string
		count int
	}
	var wf []wordFreq
	for w, c := range freq {
		wf = append(wf, wordFreq{w, c})
	}
	sort.Slice(wf, func(i, j int) bool {
		return wf[i].count > wf[j].count
	})
	var result []string
	for i, w := range wf {
		if i >= max {
			break
		}
		result = append(result, w.word)
	}
	return result
}

// renderImportantWords formats words with 🔹 prefix
func renderImportantWords(words []string) string {
	if len(words) == 0 {
		return "Nessuna parola importante rilevata."
	}
	var b strings.Builder
	for _, w := range words {
		b.WriteString("   🔹 ")
		b.WriteString(w)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
