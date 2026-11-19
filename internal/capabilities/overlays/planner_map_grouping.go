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
