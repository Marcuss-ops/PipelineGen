package clipresolver

import (
	"context"
	"fmt"
	"strings"
)

// YouTubeHarvestService abstracts YouTube search + download for auto-harvest.
//
// PR-3F: the legacy SearchTopicVideos wrapper was removed from
// internal/sources/youtube in favour of the single canonical entry
// point SearchByTopicWithFilter. Callers that need to satisfy this
// interface now expose SearchByTopicWithFilter directly.
type YouTubeHarvestService interface {
	SearchByTopicWithFilter(ctx context.Context, query string, limit int, sort, publishedAfter string) (any, error)
}

func (s *Service) enqueueHarvestForTerms(ctx context.Context, terms []string) []string {
	if s.harvestSvc == nil {
		return nil
	}

	jobIDs := make([]string, 0)
	for _, term := range terms {
		jobID, err := s.harvestSvc.EnqueueHarvest(ctx, term, 3, "youtube_1080p_7s")
		if err != nil {
			continue
		}
		if jobID != "" {
			jobIDs = append(jobIDs, jobID)
		}
	}
	return jobIDs
}

// enqueueYouTubeHarvest searches YouTube for relevant videos and enqueues
// extraction jobs for the top results. This provides a second harvest path
// alongside Artlist — when the catalog has no matching clips, we proactively
// pull from YouTube as well.
func (s *Service) enqueueYouTubeHarvest(ctx context.Context, terms []string, youtubeSvc YouTubeHarvestService) []string {
	if youtubeSvc == nil || len(terms) == 0 {
		return nil
	}

	jobIDs := make([]string, 0)
	for _, term := range terms {
		if strings.TrimSpace(term) == "" {
			continue
		}
		// Search YouTube for top 3 videos matching the term
		// PR-3F: routed through the surviving SearchByTopicWithFilter
		// (SearchTopicVideos wrapper was deleted).
		results, err := youtubeSvc.SearchByTopicWithFilter(ctx, term, 3, "", "")
		if err != nil || results == nil {
			continue
		}
		// The actual extraction is handled by the harvest service
		// which downloads + clips + uploads to Drive.
		jobID, err := s.harvestSvc.EnqueueHarvest(ctx, term, 3, "youtube_1080p_7s")
		if err != nil {
			continue
		}
		if jobID != "" {
			jobIDs = append(jobIDs, fmt.Sprintf("yt:%s", jobID))
		}
	}
	return jobIDs
}
