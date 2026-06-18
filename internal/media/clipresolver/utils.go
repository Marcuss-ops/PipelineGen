package clipresolver

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/media/clipcatalog"
	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/asset"
)

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

func (s *Service) candidateToClip(cand clipcatalog.ClipCandidate) *asset.MediaAsset {
	m := &asset.MediaAsset{
		ID:          cand.ID,
		Name:        cand.Name,
		Category:    cand.Category,
		SearchTerms: []string{cand.SearchText},
		Tags:        cand.Tags,
		SearchText:  cand.SearchText,
	}
	m.SetDriveLink(cand.DriveLink)
	m.SetLocalPath(cand.LocalPath)
	m.SetParentFolderID(cand.FolderID)
	m.SetFolderPath(cand.FolderPath)
	m.SetSceneType(cand.SceneType)
	m.SetQualityScore(cand.QualityScore)
	m.SetReuseCount(cand.ReuseCount)
	m.SetUsableFor(cand.UsableFor)
	m.SetAvoidFor(cand.AvoidFor)
	return m
}
