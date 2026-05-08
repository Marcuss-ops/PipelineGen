package clipresolver

import "context"

// HarvestPolicy determines when to enqueue harvest jobs
type HarvestPolicy struct {
	Enabled            bool    `json:"enabled"`
	MinScoreThreshold  float64 `json:"min_score_threshold"`
	MaxClipsThreshold int     `json:"max_clips_threshold"`
}

// DefaultHarvestPolicy returns the default harvest policy
func DefaultHarvestPolicy() *HarvestPolicy {
	return &HarvestPolicy{
		Enabled:           true,
		MinScoreThreshold: 0.5,
		MaxClipsThreshold: 3,
	}
}

// ShouldHarvest determines if harvesting should be triggered
func (p *HarvestPolicy) ShouldHarvest(resp *RecommendResponse) bool {
	if !p.Enabled {
		return false
	}

	// Harvest if no clips found
	if len(resp.Recommended) == 0 {
		return true
	}

	// Harvest if not enough high-quality clips
	if len(resp.Recommended) < p.MaxClipsThreshold {
		return true
	}

	// Harvest if top clip score is below threshold
	if len(resp.Recommended) > 0 && resp.Recommended[0].Score < p.MinScoreThreshold {
		return true
	}

	return false
}

// EnqueueHarvestTerms enqueues harvest jobs for terms
func (s *Service) EnqueueHarvestTerms(ctx context.Context, terms []string) error {
	if s.harvestSvc == nil {
		return nil
	}

	for _, term := range terms {
		_, err := s.harvestSvc.EnqueueHarvest(ctx, term, 3, "youtube_1080p_7s")
		if err != nil {
			// Log error but continue with other terms
			continue
		}
	}
	return nil
}
