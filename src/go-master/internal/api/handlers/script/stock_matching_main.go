package script

import (
	"fmt"
	"math"
	"sort"
	"strings"
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

	return []scoredMatch{best.match}, true
}
