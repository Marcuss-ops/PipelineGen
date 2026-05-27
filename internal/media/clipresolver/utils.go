package clipresolver

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"velox/go-master/internal/media/clipcatalog"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/pkg/textutil"
)

func (s *Service) collectSearchTerms(req *RecommendRequest) []string {
	terms := make([]string, 0)
	seen := make(map[string]bool)

	addTerm := func(term string) {
		term = strings.TrimSpace(term)
		if term == "" {
			return
		}
		lower := strings.ToLower(term)
		if !seen[lower] {
			seen[lower] = true
			terms = append(terms, term)
		}
	}

	for _, q := range req.Queries {
		addTerm(q)
	}

	if req.Topic != "" {
		addTerm(req.Topic)
	}

	if req.SegmentText != "" {
		tokens := textutil.TokenizeWithStopWords(req.SegmentText)
		for _, tok := range tokens {
			tok = strings.TrimSpace(tok)
			if len(tok) >= 4 && len(tok) > 0 && unicode.IsLetter(rune(tok[0])) {
				addTerm(tok)
				if len(terms) >= 10 {
					break
				}
			}
		}
	}

	return terms
}

func (s *Service) folderKeyFromPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	dir := filepath.Dir(path)
	if dir == "." || dir == "/" {
		return ""
	}
	return strings.ToLower(dir)
}

func (s *Service) sortRecommendations(recs []RecommendedClip) {
	n := len(recs)
	for i := 0; i < n-1; i++ {
		for j := 0; j < n-i-1; j++ {
			if recs[j].Score < recs[j+1].Score {
				recs[j], recs[j+1] = recs[j+1], recs[j]
			}
		}
	}
}

func (s *Service) buildRecommendReason(entry *ClipScore, req *RecommendRequest) string {
	reasons := make([]string, 0)

	if entry.Breakdown.TopicBoost > 0 {
		reasons = append(reasons, fmt.Sprintf("Matches topic '%s'", req.Topic))
	}
	if entry.Breakdown.CategoryBoost > 0 {
		reasons = append(reasons, fmt.Sprintf("Category '%s'", entry.Clip.Category))
	}
	if entry.MatchedQuery != "" {
		reasons = append(reasons, fmt.Sprintf("Matched query '%s'", entry.MatchedQuery))
	}
	if entry.Breakdown.NegativePenalty > 0 {
		reasons = append(reasons, "Has negative terms")
	}
	if entry.Breakdown.ReusePenalty > 0 {
		reasons = append(reasons, "Already used")
	}

	if len(reasons) == 0 {
		return "General match"
	}
	return strings.Join(reasons, "; ")
}

func (s *Service) candidateToClip(cand clipcatalog.ClipCandidate) *models.MediaAsset {
	return &models.MediaAsset{
		ID:             cand.ID,
		Name:           cand.Name,
		DriveLink:      cand.DriveLink,
		LocalPath:      cand.LocalPath,
		ParentFolderID: cand.FolderID,
		FolderPath:     cand.FolderPath,
		Category:       cand.Category,
		SearchTerms:    []string{cand.SearchText},
		Tags:           cand.Tags,
		SearchText:     cand.SearchText,
		SceneType:      cand.SceneType,
		QualityScore:   cand.QualityScore,
		ReuseCount:     cand.ReuseCount,
		UsableFor:      cand.UsableFor,
		AvoidFor:       cand.AvoidFor,
	}
}
