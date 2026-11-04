package scriptgeneration

import "time"

// PrepareJoinBreakdown decomposes the SceneTextReady join. The two branches
// start together; semantic carries VidRush preparation plus translated NLP,
// assets carries audio prefetch. WallMS is the time spent in the explicit
// prepare_join stage. The branch durations are measured from branchStart, while
// the wait fields are measured from joinStart (zero when the branch had already
// finished before the join began). LastBranch names the branch whose completion
// unblocked the join.
type PrepareJoinBreakdown struct {
	WallMS         int64  `json:"wall_ms"`
	SemanticMS     int64  `json:"semantic_ms"`
	AssetsMS       int64  `json:"assets_ms"`
	SemanticWaitMS int64  `json:"semantic_wait_ms,omitempty"`
	AssetsWaitMS   int64  `json:"assets_wait_ms,omitempty"`
	LastBranch     string `json:"last_branch"`
}

func buildPrepareJoinBreakdown(
	branchStart, joinStart, joinedAt, semanticDone, assetsDone time.Time,
) *PrepareJoinBreakdown {
	if branchStart.IsZero() || joinStart.IsZero() || joinedAt.IsZero() ||
		semanticDone.IsZero() || assetsDone.IsZero() {
		return nil
	}
	millis := func(d time.Duration) int64 {
		if d < 0 {
			return 0
		}
		return d.Milliseconds()
	}
	lastBranch := "semantic"
	if assetsDone.After(semanticDone) {
		lastBranch = "assets"
	}
	return &PrepareJoinBreakdown{
		WallMS:         millis(joinedAt.Sub(joinStart)),
		SemanticMS:     millis(semanticDone.Sub(branchStart)),
		AssetsMS:       millis(assetsDone.Sub(branchStart)),
		SemanticWaitMS: millis(semanticDone.Sub(joinStart)),
		AssetsWaitMS:   millis(assetsDone.Sub(joinStart)),
		LastBranch:     lastBranch,
	}
}
