package stockpipeline

import job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

import "fmt"

// deriveRunCounts projects completed stage outputs into the public result.
func deriveRunCounts(input *RunInput, state *RunState) RunCounts {
	var c RunCounts
	if input == nil || state == nil {
		return c
	}
	sources := concreteSources(input)
	c.RequestedVideoCount = len(sources)
	c.DiscoveredVideoCount = len(sources)
	c.SelectedVideoCount = len(uniquePlanSources(state.Plan))
	if input.DownloadMode == "sections_only" {
		stagedKeys := make(map[string]struct{}, len(state.StagedAssets))
		for _, staged := range state.StagedAssets {
			if staged != nil {
				stagedKeys[staged.SourceID] = struct{}{}
			}
		}
		for src := range uniquePlanSources(state.Plan) {
			available := false
			for _, plan := range state.Plan {
				if plan.SourceID == src {
					key := plan.StageKey
					if key == "" {
						key = plan.SourceID
					}
					if _, ok := stagedKeys[key]; ok {
						available = true
						break
					}
				}
			}
			if available {
				c.DownloadedVideoCount++
			}
		}
	} else {
		c.DownloadedVideoCount = len(state.StagedAssets)
	}
	c.ProcessedVideoCount = uniqueCutSourceCount(state.Plan, len(state.CutPaths))
	c.PlannedClipCount = len(state.Plan)
	c.CreatedClipCount = len(state.CutPaths)
	c.PublishedClipCount = len(state.Published)
	c.PersistedClipCount = len(state.CutPaths)
	if state.FinalStatus == job.StatusSucceeded {
		c.IndexedClipCount = len(state.Published)
	}
	if c.SelectedVideoCount > c.DownloadedVideoCount {
		c.FailedVideoCount = c.SelectedVideoCount - c.DownloadedVideoCount
	}
	if c.PlannedClipCount > c.CreatedClipCount {
		c.FailedClipCount = c.PlannedClipCount - c.CreatedClipCount
	}
	return c
}

// ValidateRunCounts is the fail-closed completion invariant for production
// stock runs. A successful run must account for every selected source and
// every planned clip at each durable boundary.
func ValidateRunCounts(c RunCounts) error {
	if c.DownloadedVideoCount != c.SelectedVideoCount {
		return fmt.Errorf("stock run completeness: downloaded=%d selected=%d", c.DownloadedVideoCount, c.SelectedVideoCount)
	}
	if c.CreatedClipCount != c.PlannedClipCount {
		return fmt.Errorf("stock run completeness: created=%d planned=%d", c.CreatedClipCount, c.PlannedClipCount)
	}
	if c.PublishedClipCount != c.CreatedClipCount || c.PersistedClipCount != c.CreatedClipCount || c.IndexedClipCount != c.CreatedClipCount {
		return fmt.Errorf("stock run completeness: created=%d published=%d persisted=%d indexed=%d", c.CreatedClipCount, c.PublishedClipCount, c.PersistedClipCount, c.IndexedClipCount)
	}
	if c.FailedVideoCount != 0 || c.FailedClipCount != 0 {
		return fmt.Errorf("stock run completeness: failed_videos=%d failed_clips=%d", c.FailedVideoCount, c.FailedClipCount)
	}
	return nil
}

func uniquePlanSources(plans []ClipPlan) map[string]struct{} {
	seen := make(map[string]struct{})
	for _, plan := range plans {
		seen[plan.SourceID] = struct{}{}
	}
	return seen
}

func uniqueCutSourceCount(plans []ClipPlan, cutCount int) int {
	if cutCount <= 0 {
		return 0
	}
	if n := len(uniquePlanSources(plans)); n < cutCount {
		return n
	}
	return cutCount
}
