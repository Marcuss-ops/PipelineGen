package clipresolver

import "context"

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
