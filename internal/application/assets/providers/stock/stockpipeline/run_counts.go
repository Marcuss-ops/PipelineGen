package stockpipeline

import job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

// deriveRunCounts projects completed stage outputs into the public result.
func deriveRunCounts(input *RunInput, state *runState) RunCounts {
	var c RunCounts
	if input == nil || state == nil {
		return c
	}
	sources := concreteSources(input)
	c.RequestedVideoCount = len(sources)
	c.DiscoveredVideoCount = len(sources)
	c.SelectedVideoCount = len(uniquePlanSources(state.Plan))
	c.DownloadedVideoCount = len(state.StagedAssets)
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
