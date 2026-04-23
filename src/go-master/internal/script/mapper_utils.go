package script

import (
	"sort"
	"time"

	"velox/go-master/pkg/util"
)

// autoApproveClips approva automaticamente clip con score alto
func (m *Mapper) autoApproveClips(scene *Scene) {
	now := time.Now().Format(time.RFC3339)
	
	approve := func(assignments []ClipAssignment) {
		for i := range assignments {
			if assignments[i].RelevanceScore >= m.config.AutoApproveThreshold {
				assignments[i].Status = "approved"
				assignments[i].ApprovedBy = "auto"
				assignments[i].ApprovedAt = now
			}
		}
	}

	approve(scene.ClipMapping.DriveClips)
	approve(scene.ClipMapping.ArtlistClips)
	approve(scene.ClipMapping.YouTubeClips)
	approve(scene.ClipMapping.TikTokClips)
	approve(scene.ClipMapping.StockClips)
}

// getAllClipAssignments ritorna tutte le clip assignment di una scena
func (m *Mapper) getAllClipAssignments(scene *Scene) []ClipAssignment {
	var all []ClipAssignment
	all = append(all, scene.ClipMapping.DriveClips...)
	all = append(all, scene.ClipMapping.ArtlistClips...)
	all = append(all, scene.ClipMapping.YouTubeClips...)
	all = append(all, scene.ClipMapping.TikTokClips...)
	all = append(all, scene.ClipMapping.StockClips...)
	return all
}

// deduplicateAndLimit rimuove duplicati e limita il numero
func (m *Mapper) deduplicateAndLimit(clips []ClipAssignment, limit int) []ClipAssignment {
	seen := make(map[string]bool)
	var unique []ClipAssignment

	for _, clip := range clips {
		if !seen[clip.ClipID] {
			seen[clip.ClipID] = true
			unique = append(unique, clip)
		}
	}

	sort.Slice(unique, func(i, j int) bool {
		return unique[i].RelevanceScore > unique[j].RelevanceScore
	})

	if limit > 0 && len(unique) > limit {
		return unique[:limit]
	}

	return unique
}

// GetApprovalRequests ottiene le scene che richiedono approvazione
func (m *Mapper) GetApprovalRequests(script *StructuredScript) []ClipApprovalRequest {
	var requests []ClipApprovalRequest

	for _, scene := range script.Scenes {
		if scene.Status == SceneNeedsReview || scene.Status == SceneClipsFound {
			allClips := m.getAllClipAssignments(&scene)

			var candidates []ClipCandidate
			var autoApproved []string

			for _, clip := range allClips {
				candidate := ClipCandidate{
					ClipID:         clip.ClipID,
					Source:         clip.Source,
					RelevanceScore: clip.RelevanceScore,
					MatchReason:    clip.MatchReason,
					URL:            clip.URL,
					Duration:       clip.Duration,
				}

				if clip.RelevanceScore >= m.config.AutoApproveThreshold {
					candidate.Recommendation = "approve"
					autoApproved = append(autoApproved, clip.ClipID)
				} else if clip.RelevanceScore >= m.config.MinScore {
					candidate.Recommendation = "review"
				} else {
					candidate.Recommendation = "reject"
				}

				candidates = append(candidates, candidate)
			}

			requests = append(requests, ClipApprovalRequest{
				SceneNumber:  scene.SceneNumber,
				SceneText:    scene.Text[:util.Min(200, len(scene.Text))],
				Clips:        candidates,
				NeedsReview:  len(autoApproved) == 0,
				AutoApproved: autoApproved,
			})
		}
	}

	return requests
}
