package overlays

import (
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

// overlayItemWindowUS is the start/end projection of the package's single
// start/duration window helper, so the two cannot disagree on which field is
// authoritative.
// OverlayItemWindowUS is the canonical start/end projection. It is exported
// because the render-queue boundary must not re-derive which timing field is
// authoritative.
func OverlayItemWindowUS(item capmodel.OverlayItem) (int64, int64) {
	start, duration := item.StartUS, item.DurationUS
	if duration <= 0 {
		start = item.StartMs * 1000
		duration = (item.EndMs - item.StartMs) * 1000
	}
	return start, start + duration
}
