package overlays

import (
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/geodesy"
)

// coalesceMapPlansForNarrativeGrouping gives every place of one narrative
// group the same certified plate when that plate covers all of them. The
// resolver can return a distinct plate per geocoded point (one centered plate
// per city); grouping by plate id then makes narrative grouping unreachable
// and every city becomes a separate map. Reusing a covering plate preserves
// provenance and lets the pins share one composition.
func coalesceMapPlansForNarrativeGrouping(plans []MapPlan, grouping MapGroupConfig) []MapPlan {
	if len(plans) < 2 || grouping.NoNarrativeGrouping {
		return plans
	}
	common := plans[0].Plate
	if common.Zoom <= 0 || common.Width <= 0 || common.Height <= 0 {
		return plans
	}
	window := common.Window
	if window.Width <= 0 || window.Height <= 0 {
		window = geodesy.CenteredOn(common.Center.Latitude, common.Center.Longitude, common.Zoom, common.Width, common.Height)
	}
	for _, plan := range plans {
		if !window.Contains(plan.Place.Latitude, plan.Place.Longitude) {
			return plans
		}
	}
	for i := range plans {
		plans[i].Plate = common
	}
	return plans
}

// mergeShortFinalMapChunk folds a final map chunk shorter than the readable
// floor into the preceding chunk of the same plate/group. The merged item
// keeps every pin and the spoken order; it is rejected when the pin cap or the
// presentation ceiling would be exceeded. This is what turns the 0.94 s
// Siena/Campo tail into one readable two-pin map instead of a flash.
func mergeShortFinalMapChunk(chunks [][]MapCandidate, maxPins int, maxSpanUS int64) [][]MapCandidate {
	if len(chunks) < 2 || maxPins <= 0 || maxSpanUS <= 0 {
		return chunks
	}
	last := chunks[len(chunks)-1]
	previous := chunks[len(chunks)-2]
	if len(last) == 0 || len(previous) == 0 {
		return chunks
	}
	lastStart, lastEnd := mapChunkSpan(last)
	previousStart, _ := mapChunkSpan(previous)
	if lastEnd-lastStart >= MapMinimumReadableUS {
		return chunks
	}
	if len(previous)+len(last) > maxPins {
		// The tail is short but the preceding chunk is already at the pin
		// cap. Move the most recent trailing pins of the previous chunk to
		// the front of the tail: both chunks stay within the cap and the
		// tail gets a readable spoken span (Siena + Campo instead of a
		// three-pin map followed by a 0.94 s flash).
		overflow := len(previous) + len(last) - maxPins
		if overflow <= 0 || overflow >= len(previous) {
			return chunks
		}
		moved := append([]MapCandidate(nil), previous[len(previous)-overflow:]...)
		previous = previous[:len(previous)-overflow]
		last = append(moved, last...)
		chunks[len(chunks)-2] = previous
		chunks[len(chunks)-1] = last
		return chunks
	}
	if lastEnd-previousStart > maxSpanUS {
		return chunks
	}
	merged := make([]MapCandidate, 0, len(previous)+len(last))
	merged = append(merged, previous...)
	merged = append(merged, last...)
	chunks[len(chunks)-2] = merged
	return chunks[:len(chunks)-1]
}

func mapChunkSpan(chunk []MapCandidate) (startUS, endUS int64) {
	first := true
	for _, candidate := range chunk {
		candidateStart := candidate.StartUS
		candidateEnd := candidate.StartUS + candidate.DurationUS
		if first || candidateStart < startUS {
			startUS = candidateStart
		}
		if first || candidateEnd > endUS {
			endUS = candidateEnd
		}
		first = false
	}
	return startUS, endUS
}
