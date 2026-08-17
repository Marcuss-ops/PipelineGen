package wiring

import "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

// resolveClipDurationSource resolves the canonical provenance for a resolved
// clip duration. renderAssetDurationSeconds already probed the local binary
// when the catalog had no duration, so an unknown provenance here means a
// fresh probe measurement (never a fabricated 0).
func resolveClipDurationSource(canonical *asset.Asset) asset.DurationSource {
	source := canonical.DurationProvenance()
	if source == asset.DurationUnknown {
		// The catalog had no duration, so renderAssetDurationSeconds probed
		// the local binary — an authoritative measurement.
		return asset.DurationProbe
	}
	return source
}
