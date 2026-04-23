package channelmonitor

import (
	"context"
	"math"
	"regexp"
	"sort"
	"strings"

	"velox/go-master/internal/upload/drive"
)

// extractProtagonist extracts the main subject/person name from a video title.
func extractProtagonist(title string) string {
	cleaned := normalizeWhitespace(removeBracketed(title))
	cleaned = cutAtSeparator(cleaned)
	tokens := strings.Fields(cleaned)
	if len(tokens) == 0 {
		return ""
	}

	var picked []string
	seenName := false
	for _, tok := range tokens {
		trimTok := strings.Trim(tok, ".,:;!?\"'")
		if trimTok == "" {
			continue
		}
		lower := strings.ToLower(trimTok)

		if (isNoiseWord(lower) || isConnector(lower)) && seenName {
			break
		}
		if looksLikeNameToken(trimTok) {
			picked = append(picked, trimTok)
			seenName = true
			if len(picked) >= 4 {
				break
			}
			continue
		}
		if seenName {
			break
		}
	}

	if len(picked) >= 2 {
		return sanitizeFolderName(strings.Join(picked, " "))
	}

	legacy := regexp.MustCompile(`(?i)\b(official\s+(music\s+)?video|lyrics?\s*video|audio|ft\.?\s+\w+|feat\.?\s+.+?)\b`).ReplaceAllString(cleaned, "")
	legacy = regexp.MustCompile(`[^a-zA-Z0-9\s'&-]`).ReplaceAllString(legacy, "")
	legacy = normalizeWhitespace(legacy)
	legacy = trimTrailingNoise(legacy)

	if idx := strings.Index(legacy, " - "); idx > 0 {
		name := strings.TrimSpace(legacy[:idx])
		if isValidName(name) {
			return name
		}
	}
	if idx := strings.Index(legacy, ":"); idx > 0 {
		name := strings.TrimSpace(legacy[:idx])
		if isValidName(name) {
			return name
		}
	}

	vsPatterns := regexp.MustCompile(`(?i)\b(?:vs\.?|and|&)\b`)
	if vsPatterns.MatchString(legacy) {
		parts := vsPatterns.Split(legacy, 2)
		if len(parts) == 2 {
			name := strings.TrimSpace(parts[0])
			if isValidName(name) {
				return name
			}
		}
	}

	words := strings.Fields(legacy)
	if len(words) <= 4 {
		return legacy
	}

	re2 := regexp.MustCompile(`([A-Z][a-z]+(?:\s+[A-Z][a-z]+)*)`)
	matches := re2.FindAllString(legacy, -1)
	if len(matches) > 0 {
		longest := ""
		for _, m := range matches {
			if len(m) > len(longest) {
				longest = m
			}
		}
		if isValidName(longest) {
			return longest
		}
	}

	if len(words) >= 2 {
		return strings.Join(words[:2], " ")
	}
	if len(words) == 1 {
		return words[0]
	}

	return legacy
}

func (m *Monitor) findBestProtagonistFolder(ctx context.Context, categoryFolderID, candidate string) (string, string, float64, bool) {
	if strings.TrimSpace(candidate) == "" {
		return "", "", 0, false
	}
	result, err := m.driveClient.ListFolders(ctx, drive.ListFoldersOptions{
		ParentID: categoryFolderID,
		MaxDepth: 1,
		MaxItems: 500,
	})
	if err != nil {
		return "", "", 0, false
	}

	bestScore := 0.0
	bestName := ""
	bestID := ""
	for _, f := range result {
		score := nameSimilarityScore(candidate, f.Name)
		if score > bestScore {
			bestScore = score
			bestName = f.Name
			bestID = f.ID
		}
	}
	if bestName == "" {
		return "", "", 0, false
	}
	return bestName, bestID, bestScore, true
}

func nameSimilarityScore(a, b string) float64 {
	nA := normalizeProtagonistKey(a)
	nB := normalizeProtagonistKey(b)
	if nA == "" || nB == "" {
		return 0
	}
	if nA == nB {
		return 1
	}

	score := tokenJaccard(nA, nB)
	if strings.Contains(nA, nB) || strings.Contains(nB, nA) {
		score = math.Max(score, 0.92)
	}
	if leadingTokensEqual(a, b, 2) {
		score = math.Max(score, 0.90)
	}
	return math.Min(score, 1)
}

func normalizeProtagonistKey(name string) string {
	s := strings.ToLower(name)
	s = regexp.MustCompile(`[^a-z0-9\s]`).ReplaceAllString(s, " ")
	s = normalizeWhitespace(s)
	if s == "" {
		return ""
	}
	var out []string
	for _, t := range strings.Fields(s) {
		if isNoiseWord(t) || len(t) <= 1 {
			continue
		}
		out = append(out, t)
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

func tokenJaccard(a, b string) float64 {
	as := strings.Fields(a)
	bs := strings.Fields(b)
	if len(as) == 0 || len(bs) == 0 {
		return 0
	}
	setA := make(map[string]struct{}, len(as))
	setB := make(map[string]struct{}, len(bs))
	for _, t := range as {
		setA[t] = struct{}{}
	}
	for _, t := range bs {
		setB[t] = struct{}{}
	}
	inter := 0
	for t := range setA {
		if _, ok := setB[t]; ok {
			inter++
		}
	}
	union := len(setA) + len(setB) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func leadingTokensEqual(a, b string, n int) bool {
	ta := strings.Fields(strings.ToLower(normalizeWhitespace(a)))
	tb := strings.Fields(strings.ToLower(normalizeWhitespace(b)))
	if len(ta) < n || len(tb) < n {
		return false
	}
	for i := 0; i < n; i++ {
		if ta[i] != tb[i] {
			return false
		}
	}
	return true
}

func isNoiseWord(token string) bool {
	_, ok := protagonistNoiseWords[strings.ToLower(strings.TrimSpace(token))]
	return ok
}

func isConnector(token string) bool {
	switch strings.ToLower(token) {
	case "-", "|", ":", "vs", "v", "and", "&":
		return true
	default:
		return false
	}
}

func looksLikeNameToken(token string) bool {
	if token == "" {
		return false
	}
	r := rune(token[0])
	if r >= 'A' && r <= 'Z' {
		return true
	}
	allUpper := true
	for _, ch := range token {
		if ch >= 'a' && ch <= 'z' {
			allUpper = false
			break
		}
	}
	return allUpper && len(token) >= 2
}

func removeBracketed(s string) string {
	re := regexp.MustCompile(`[\(\[\{][^\)\]\}]*[\)\]\}]`)
	return re.ReplaceAllString(s, " ")
}

func normalizeWhitespace(s string) string {
	s = regexp.MustCompile(`\s+`).ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func cutAtSeparator(s string) string {
	separators := []string{" | ", " - ", " : "}
	for _, sep := range separators {
		if idx := strings.Index(s, sep); idx > 0 {
			return strings.TrimSpace(s[:idx])
		}
	}
	return s
}

func trimTrailingNoise(s string) string {
	parts := strings.Fields(s)
	if len(parts) == 0 {
		return s
	}
	end := len(parts)
	for end > 0 {
		if isNoiseWord(parts[end-1]) {
			end--
			continue
		}
		break
	}
	if end == 0 {
		return s
	}
	return strings.Join(parts[:end], " ")
}

func isValidName(name string) bool {
	if len(name) < 2 || len(name) > 50 {
		return false
	}
	words := strings.Fields(name)
	for _, w := range words {
		if len(w) > 0 && w[0] >= 'A' && w[0] <= 'Z' {
			return true
		}
	}
	return false
}

func sanitizeFolderName(name string) string {
	re := regexp.MustCompile(`[<>:"/\\|?*]`)
	cleaned := re.ReplaceAllString(name, "")
	cleaned = regexp.MustCompile(`[\x00-\x1f]`).ReplaceAllString(cleaned, "")
	cleaned = strings.TrimSpace(cleaned)
	cleaned = regexp.MustCompile(`\s+`).ReplaceAllString(cleaned, " ")
	if len(cleaned) > 100 {
		cleaned = cleaned[:100]
	}
	if cleaned == "" {
		return "Unnamed"
	}
	return cleaned
}
