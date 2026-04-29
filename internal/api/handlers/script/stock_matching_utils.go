package script

import (
	"strings"
	"unicode"
)

func normalizeMatchText(text string) string {
	return strings.ToLower(strings.TrimSpace(text))
}

func matchTokens(text string) []string {
	text = normalizeMatchText(text)
	words := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	return words
}

func bm25FolderScore(tokens []string, rec stockFolderMatchRecord, idx *stockFolderMatchIndex) float64 {
	if idx == nil || len(tokens) == 0 {
		return 0
	}
	score := 0.0
	k1 := 1.5
	b := 0.75
	for _, term := range tokens {
		tf := float64(rec.Counts[term])
		dl := float64(rec.Length)
		avgdl := idx.AvgLen
		if avgdl == 0 {
			avgdl = 1
		}
		idf := idx.IDF(term)
		score += idf * (tf / (tf + k1*(1-b+b*dl/avgdl)))
	}
	return score
}

func cosineFolderScore(tokens []string, rec stockFolderMatchRecord) float64 {
	if len(tokens) == 0 {
		return 0
	}
	dot := 0.0
	queryVec := make(map[string]float64)
	for _, t := range tokens {
		queryVec[t]++
	}
	docVec := rec.Counts
	normQ := 0.0
	for _, v := range queryVec {
		normQ += v * v
	}
	normD := 0.0
	for _, v := range docVec {
		normD += float64(v) * float64(v)
	}
	if normQ == 0 || normD == 0 {
		return 0
	}
	for t, qv := range queryVec {
		dot += qv * float64(docVec[t])
	}
	return dot / (sqrt(normQ) * sqrt(normD))
}

func fuzzyEntityFolderScore(entities []string, normKey string) float64 {
	if len(entities) == 0 || normKey == "" {
		return 0
	}
	score := 0.0
	for _, e := range entities {
		if strings.Contains(normKey, strings.ToLower(e)) {
			score += 1.0
		}
	}
	return score / float64(len(entities))
}

func preferredPathScore(preferredPaths []string, rec stockFolderMatchRecord) float64 {
	if len(preferredPaths) == 0 {
		return 0
	}
	path := rec.Folder.StockPath()
	for _, p := range preferredPaths {
		if strings.Contains(path, p) {
			return 1.0
		}
	}
	return 0
}

func preferredGroupScore(preferredGroup string, rec stockFolderMatchRecord) float64 {
	if preferredGroup == "" {
		return 0
	}
	if strings.Contains(strings.ToLower(rec.Folder.Group), preferredGroup) {
		return 1.0
	}
	return 0
}

func sqrt(x float64) float64 {
	if x == 0 {
		return 0
	}
	r := x
	for i := 0; i < 10; i++ {
		r = (r + x/r) / 2
	}
	return r
}
