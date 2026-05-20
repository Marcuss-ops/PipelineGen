package script

import (
	"fmt"
	"sort"
	"strings"
)

// extractImportantWords is now a simple fallback using length and frequency.
// The main logic has moved to LLM extraction in BuildScriptDocument.
func extractImportantWords(narrative string, max int) []string {
	words := strings.Fields(strings.ToLower(narrative))
	freq := make(map[string]int)
	for _, w := range words {
		w = strings.Trim(w, ",.!?;:\"'()")
		// Simple logic: ignore words shorter than 4 chars (most prepositions/articles)
		if len([]rune(w)) < 4 {
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
		if wf[i].count == wf[j].count {
			return wf[i].word < wf[j].word
		}
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

// renderImportantWords formats words with 🔹 prefix and optional image links.
func renderImportantWords(words []string, images map[string]string) string {
	if len(words) == 0 {
		return "Nessuna parola importante rilevata."
	}
	var b strings.Builder
	for _, w := range words {
		b.WriteString("   🔹 ")
		b.WriteString(w)
		b.WriteString("\n")
		if link := strings.TrimSpace(images[strings.ToLower(strings.TrimSpace(w))]); link != "" {
			b.WriteString(fmt.Sprintf("      🖼️ %s\n", link))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
