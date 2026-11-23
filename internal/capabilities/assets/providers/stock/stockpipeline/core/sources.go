package core

// concreteSources returns the run inputs as concrete VideoSource values in
// canonical priority order.
func ConcreteSources(input *RunInput) []VideoSource {
	if input == nil {
		return nil
	}
	if len(input.DirectURLs) > 0 {
		sources := make([]VideoSource, 0, len(input.DirectURLs))
		for _, raw := range input.DirectURLs {
			if raw == "" {
				continue
			}
			// Carry the provider-known duration (populated by
			// resolveInputQueries) so the deterministic planner uses
			// the real source length instead of the budget*10
			// fallback. Unknown durations remain zero and keep the
			// planner's conservative fallback + extract-time bounds
			// check contract.
			duration := float64(0)
			if input.SourceDurations != nil {
				duration = input.SourceDurations[raw]
			}
			sources = append(sources, VideoSource{URL: raw, Title: raw, Source: raw, DurationSec: duration})
		}
		return sources
	}
	if src, ok := FirstSource(input); ok {
		return []VideoSource{src}
	}
	return nil
}

// firstSource returns the first source the orchestrator can plan
// against. Used by Run as a Commit 1 round-trip target.
//
// Priority: DirectURLs > DriveURLs > SearchQueries.
// DriveURLs are YouTube URLs that should be downloaded via yt-dlp
// (Google Drive download is a forward-pointer for StockStager).
func FirstSource(input *RunInput) (VideoSource, bool) {
	if input == nil {
		return VideoSource{}, false
	}
	if len(input.DirectURLs) > 0 {
		return VideoSource{
			URL:    input.DirectURLs[0],
			Title:  "demo-direct",
			Source: input.DirectURLs[0],
		}, true
	}
	if len(input.DriveURLs) > 0 {
		return VideoSource{
			URL:    input.DriveURLs[0],
			Title:  "demo-drive",
			Source: input.DriveURLs[0],
		}, true
	}
	if len(input.SearchQueries) > 0 {
		return VideoSource{
			URL:    input.SearchQueries[0],
			Title:  "demo-query",
			Source: input.SearchQueries[0],
		}, true
	}
	return VideoSource{}, false
}
