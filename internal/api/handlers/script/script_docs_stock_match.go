package script

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

func rankStockFoldersForSegment(idx *stockFolderMatchIndex, seg TimelineSegment) ([]scoredMatch, bool) {
	if idx == nil || len(idx.Records) == 0 {
		return nil, false
	}

	queryText := strings.Join([]string{
		seg.OpeningSentence,
		seg.ClosingSentence,
		strings.Join(seg.Keywords, " "),
		strings.Join(seg.Entities, " "),
	}, " ")
	queryText = normalizeMatchText(queryText)
	queryTokens := matchTokens(queryText)
	queryEntities := uniqueStrings(append(extractLikelyNames(seg.OpeningSentence), extractLikelyNames(seg.ClosingSentence)...))
	if len(queryEntities) == 0 {
		queryEntities = extractLikelyNames(strings.Join([]string{seg.OpeningSentence, seg.ClosingSentence, strings.Join(seg.Entities, " ")}, " "))
	}
	preferredPaths := uniqueStrings(seg.PreferredStockPaths)
	preferredGroup := normalizeMatchText(seg.PreferredStockGroup)

	type candidate struct {
		score     float64
		bm25      float64
		cosine    float64
		fuzzy     float64
		preferred float64
		group     float64
		match     scoredMatch
	}

	candidates := make([]candidate, 0, len(idx.Records))
	for _, rec := range idx.Records {
		bm25 := bm25FolderScore(queryTokens, rec, idx)
		cosine := cosineFolderScore(queryTokens, rec)
		fuzzy := fuzzyEntityFolderScore(queryEntities, rec.NormKey)
		preferred := preferredPathScore(preferredPaths, rec)
		groupBoost := preferredGroupScore(preferredGroup, rec)
		score := 0.3*bm25 + 0.3*cosine + 0.15*fuzzy + 0.15*preferred + 0.1*groupBoost

		title := strings.TrimSpace(rec.Folder.StockPath())
		if title == "" {
			title = strings.TrimSpace(rec.Folder.FullPath)
		}
		candidates = append(candidates, candidate{
			score:     score,
			bm25:      bm25,
			cosine:    cosine,
			fuzzy:     fuzzy,
			preferred: preferred,
			group:     groupBoost,
			match: scoredMatch{
				Title:   title,
				Path:    rec.Folder.StockPath(),
				Score:   int(math.Round(score * 1000)),
				Source:  "stock folder index hybrid",
				Link:    rec.Folder.PickLink(),
				Details: fmt.Sprintf("bm25=%.2f cosine=%.2f fuzzy=%.2f group=%.2f", bm25, cosine, fuzzy, groupBoost),
			},
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].match.Title < candidates[j].match.Title
		}
		return candidates[i].score > candidates[j].score
	})

	best := candidates[0]
	second := candidate{}
	if len(candidates) > 1 {
		second = candidates[1]
	}

	if best.cosine < 0.72 && best.preferred < 0.75 && best.group < 0.75 {
		return nil, false
	}
	if len(candidates) > 1 && second.score > 0 && best.score < second.score*1.3 && best.preferred < 0.75 && best.group < 0.75 {
		return nil, false
	}
	if best.score < 0.72 && best.preferred < 0.75 && best.group < 0.75 {
		return nil, false
	}

	return []scoredMatch{best.match}, true
}

func bm25FolderScore(queryTokens []string, rec stockFolderMatchRecord, idx *stockFolderMatchIndex) float64 {
	if len(queryTokens) == 0 || rec.Length == 0 || idx == nil || idx.AvgLen == 0 {
		return 0
	}
	const k1 = 1.2
	const b = 0.75
	score := 0.0
	seen := make(map[string]struct{}, len(queryTokens))
	for _, term := range queryTokens {
		if _, ok := seen[term]; ok {
			continue
		}
		seen[term] = struct{}{}
		tf := rec.Counts[term]
		if tf == 0 {
			continue
		}
		idf := idx.IDF(term)
		if idf == 0 {
			continue
		}
		norm := float64(tf) * (k1 + 1)
		den := float64(tf) + k1*(1-b+b*float64(rec.Length)/idx.AvgLen)
		score += idf * (norm / den)
	}
	return score / (score + 2.5)
}

func cosineFolderScore(queryTokens []string, rec stockFolderMatchRecord) float64 {
	if len(queryTokens) == 0 || rec.Length == 0 {
		return 0
	}
	qCounts := make(map[string]int, len(queryTokens))
	for _, tok := range queryTokens {
		qCounts[tok]++
	}
	var dot, qNorm, dNorm float64
	for tok, qtf := range qCounts {
		qw := float64(qtf)
		dw := float64(rec.Counts[tok])
		dot += qw * dw
		qNorm += qw * qw
		dNorm += dw * dw
	}
	if dot == 0 || qNorm == 0 || dNorm == 0 {
		return 0
	}
	return dot / (math.Sqrt(qNorm) * math.Sqrt(dNorm))
}

func fuzzyEntityFolderScore(entities []string, normKey string) float64 {
	if len(entities) == 0 || normKey == "" {
		return 0
	}
	best := 0.0
	for _, entity := range entities {
		entity = normalizeMatchText(entity)
		if entity == "" {
			continue
		}
		score := fuzzySimilarity(entity, normKey)
		if score > best {
			best = score
		}
	}
	return best
}

func preferredPathScore(preferredPaths []string, rec stockFolderMatchRecord) float64 {
	if len(preferredPaths) == 0 {
		return 0
	}
	candidate := normalizeMatchText(strings.Join([]string{
		rec.Folder.StockPath(),
		rec.Folder.FullPath,
		rec.Folder.TopicSlug,
		rec.Folder.FolderID,
	}, " "))
	if candidate == "" {
		return 0
	}
	best := 0.0
	for _, path := range preferredPaths {
		path = normalizeMatchText(path)
		if path == "" {
			continue
		}
		score := fuzzySimilarity(path, candidate)
		if score > best {
			best = score
		}
	}
	return best
}

func preferredGroupScore(preferredGroup string, rec stockFolderMatchRecord) float64 {
	preferredGroup = strings.TrimSpace(preferredGroup)
	if preferredGroup == "" {
		return 0
	}
	group := normalizeMatchText(folderGroupFromStockRecord(rec))
	if group == "" {
		return 0
	}
	return fuzzySimilarity(preferredGroup, group)
}

func folderGroupFromStockRecord(rec stockFolderMatchRecord) string {
	return folderGroupFromPath(rec.Folder.StockPath())
}

func fuzzySimilarity(a, b string) float64 {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1
	}
	if strings.Contains(b, a) || strings.Contains(a, b) {
		shorter := math.Min(float64(len(a)), float64(len(b)))
		longer := math.Max(float64(len(a)), float64(len(b)))
		if longer == 0 {
			return 0
		}
		return shorter / longer
	}
	dist := levenshteinDistance(a, b)
	longer := math.Max(float64(len(a)), float64(len(b)))
	if longer == 0 {
		return 0
	}
	sim := 1 - float64(dist)/longer
	if sim < 0 {
		return 0
	}
	return sim
}

func levenshteinDistance(a, b string) int {
	ar := []rune(a)
	br := []rune(b)
	if len(ar) == 0 {
		return len(br)
	}
	if len(br) == 0 {
		return len(ar)
	}
	prev := make([]int, len(br)+1)
	curr := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i, ra := range ar {
		curr[0] = i + 1
		for j, rb := range br {
			cost := 0
			if ra != rb {
				cost = 1
			}
			insert := curr[j] + 1
			delete := prev[j+1] + 1
			replace := prev[j] + cost
			curr[j+1] = minInt(insert, minInt(delete, replace))
		}
		prev, curr = curr, prev
	}
	return prev[len(br)]
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (idx *stockFolderMatchIndex) IDF(term string) float64 {
	if idx == nil || term == "" {
		return 0
	}
	df := idx.DF[term]
	if df == 0 {
		return 0
	}
	if float64(df)/float64(len(idx.Records)) > 0.4 {
		return 0
	}
	return math.Log((float64(len(idx.Records))+1)/(float64(df)+1)) + 1
}

func normalizeMatchText(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return ""
	}
	text = norm.NFD.String(text)
	var b strings.Builder
	for _, r := range text {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			continue
		}
		b.WriteRune(' ')
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func matchTokens(text string) []string {
	text = normalizeMatchText(text)
	if text == "" {
		return nil
	}
	parts := strings.Fields(text)
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		if len(part) < 3 || isStopWord(part) {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		out = append(out, part)
	}
	return out
}
