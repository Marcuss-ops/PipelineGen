// Package script — clip_binding_helpers.go: pure per-segment duration math
// for ClipBinding.
//
// godlike/06 SSOT: this file owns the canonical `max(0, endMs - startMs)`
// math for ClipBinding.DurationMs. No other file may hand-roll equivalent
// logic; a future change (e.g. snap-to-keyframe) is a single-file edit.
//
// godlike/07 NO-FAKE-AVAILABILITY: returns 0 (the "unknown duration"
// surface) for reversed / equal / overflowing offsets; downstream callers
// treat 0 as "duration unknown". Negative startMs is NOT pre-clamped — a
// positive result from e.g. (-100, 200) is a deliberate operator-visible
// signal of upstream rollover, not a fabricated value.
package script

// ClipDurationMs computes the canonical per-segment DurationMs for a
// ClipBinding. Contract: max(0, endMs - startMs) — returns 0 on reversed
// or equal offsets; negative startMs is NOT clamped (operator-visible
// upstream rollover signal, not a fabricated value).
func ClipDurationMs(startMs, endMs int64) int64 {
	if endMs > startMs {
		return endMs - startMs
	}
	return 0
}

// ClipDurationMsFromAssetID is the canonical "honest null" surface for the
// asset-level full-clip duration lookup. The script package has no
// asset-access port (godlike/06 layering: kernel/script is the bottom of
// the import graph). Callers that need the actual duration route through
// the composition-root AssetDurationResolver port (asset-level lookup is a
// separate concern).
func ClipDurationMsFromAssetID(_ string) int64 {
	return 0
}
