package artlist

import (
	"velox/go-master/pkg/models"
)

// selectCandidates selects up to limit clips from the list
func (s *Service) selectCandidates(clipsList []*models.Clip, limit int) []*models.Clip {
	candidateClips := clipsList
	if len(candidateClips) > limit {
		candidateClips = candidateClips[:limit]
	}
	return candidateClips
}
