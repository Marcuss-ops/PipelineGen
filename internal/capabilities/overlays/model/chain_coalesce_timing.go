package model

// chainExceedsSceneEnd reports whether the sequentially laid-out chain would
// end past its owning scene. A zero/unknown scene end keeps the legacy path.
func chainExceedsSceneEnd(group []OverlayItem, sceneEndUS int64) bool {
	if sceneEndUS <= 0 || len(group) < 2 {
		return false
	}
	return projectedChainEndUS(group) > sceneEndUS
}

// projectedChainEndUS mirrors fuseChain's cursor arithmetic: the first segment
// keeps the group start, later segments follow after the handoff gap, and
// image/card segments get the same minimum hold as the fused output.
func projectedChainEndUS(group []OverlayItem) int64 {
	if len(group) == 0 {
		return 0
	}
	start := group[0].StartUSValue()
	families := make(map[string]bool, 2)
	for _, item := range group {
		if itemStart := item.StartUSValue(); itemStart < start {
			start = itemStart
		}
		families[chainFamily(item.Kind)] = true
	}
	cursor := start
	for index, item := range group {
		duration := item.EndUSValue() - item.StartUSValue()
		if (chainFamily(item.Kind) != "text" || families["map"]) && duration < minChainSegmentDurationUS {
			duration = minChainSegmentDurationUS
		}
		if index > 0 {
			cursor += chainHandoffGapUS
		}
		cursor += duration
	}
	return cursor
}
