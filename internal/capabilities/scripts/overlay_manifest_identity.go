package scriptgeneration

// overlay_manifest_identity.go owns the manifest-entry helpers shared by the
// final-job overlay certificate: window tolerance, packed positions and
// date-number recovery. The local producer renders per item; the final-job
// handoff still consumes a remote manifest when one is supplied.

import (
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"strconv"
	"strings"
)

// isOverlayBackgroundTemplate is the single source of truth for the legacy
// background spelling: a background is a canvas primitive, not a user-facing
// overlay. It must never become its own rendered item, manifest entry or
// Drive video.
func isOverlayBackgroundTemplate(templateID string) bool {
	switch strings.ToUpper(strings.TrimSpace(templateID)) {
	case "BACKGROUND", "VIDEO_BACKGROUND":
		return true
	default:
		return false
	}
}

// overlayRenderableCandidates is the single admission list for overlay render
// work: every plan item except background canvas primitives, in plan order
// with its original plan index preserved (child plan identity depends on it).
func overlayRenderableCandidates(plan capmodel.OverlayPlan) []overlayItemCandidate {
	candidates := make([]overlayItemCandidate, 0, len(plan.Items))
	for index, item := range plan.Items {
		if isOverlayBackgroundTemplate(item.TemplateID) {
			continue
		}
		candidates = append(candidates, overlayItemCandidate{index: index, source: item})
	}
	return candidates
}

// finalJobArtifactWindowTolerance is how far a manifest window may overhang the
// artifact's reported duration before the certificate calls it a defect. It is
// exactly one frame: `out_us` is ceil'd to a frame edge, the duration is a frame
// count, and neither side is allowed to grow past that rounding. An unknown
// frame rate yields 0, which keeps the strict comparison.
func finalJobArtifactWindowTolerance(fpsNum, fpsDen int) int64 {
	if fpsNum <= 0 || fpsDen <= 0 {
		return 0
	}
	return int64(1_000_000) * int64(fpsDen) / int64(fpsNum)
}

// manifestEntryArtifactPositions reports the entry's packed positions and
// whether they are distinct from its plan receipt.
func manifestEntryArtifactPositions(entry capmodel.OverlayManifestEntry) (int64, int64, bool) {
	if entry.ArtifactInUS == nil || entry.ArtifactOutUS == nil {
		return 0, 0, false
	}
	return *entry.ArtifactInUS, *entry.ArtifactOutUS, true
}

func finalJobManifestYearItem(entry capmodel.OverlayManifestEntry, items []capmodel.OverlayItem) (capmodel.OverlayItem, bool) {
	parts := strings.Split(strings.TrimSpace(entry.ItemID), "__")
	if len(parts) != 4 || parts[2] != "date_number" {
		return capmodel.OverlayItem{}, false
	}
	if _, err := strconv.Atoi(parts[1]); err != nil {
		return capmodel.OverlayItem{}, false
	}
	var match capmodel.OverlayItem
	found := false
	for _, item := range items {
		if !strings.EqualFold(strings.TrimSpace(item.Kind), "number") || strings.TrimSpace(item.Text) != parts[1] {
			continue
		}
		startUS, endUS := item.StartUSValue(), item.EndUSValue()
		if entry.InUS >= endUS || startUS >= entry.OutUS {
			continue
		}
		if found {
			return capmodel.OverlayItem{}, false
		}
		match, found = item, true
	}
	return match, found
}
