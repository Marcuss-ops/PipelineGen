// Package script — clip_binding_helpers.go: pure clip-binding
// duration computation helpers.
//
// godlike/06 SSOT (one canonical owner per fact):
//   - per-segment duration derivation (`max(0, endMs - startMs)`)
//     for ClipBinding.DurationMs — THIS file's `ClipDurationMs`.
//     No other file in the project may hand-roll
//     `if endMs > startMs { return endMs - startMs }; return 0`
//     against ClipBinding offsets; the math lives ONLY here so
//     a future evolution (e.g. snapping to keyframe boundaries)
//     is a single-file edit.
//
//   - per-asset-level full-clip duration lookup when offset info
//     is absent — NOT this file. Asset-level lookup is a
//     separate concern owned by the composition-root adapter
//     (`AssetDurationResolver` port wired at internal/app/),
//     per the FASE-2 godlike/06 SSOT note on
//     `ClipBinding.DurationMs` in model_output.go. This file's
//     `ClipDurationMsFromAssetID` deliberately returns 0 (the
//     "honest null" idiom — godlike/07 NO-FAKE-AVAILABILITY)
//     because internal/domain/script has no asset-access port;
//     callers that need the asset-level number route through
//     the composition adapter instead.
package script

// ClipDurationMs computes the canonical per-segment DurationMs
// for a ClipBinding. The contract is `max(0, endMs - startMs)` —
// a negative-or-reversed segment is surfaced as 0 (downstream
// consumers treat 0 as "duration unknown" per the ClipBinding
// godlike/07 NO-FAKE-AVAILABILITY note in model_output.go).
//
// godlike/06 literal SSOT: the implementation is the literal
// `max(0, endMs - startMs)` math WITHOUT a defensive clamp on
// negative `startMs`. The reasoning:
//   - The spec is `max(0, endMs - startMs)`, full stop. A
//     defensive pre-clamp SURFACES a phantom non-zero
//     DurationMs (e.g. (-100, 200) → user believes the segment
//     is 200ms when in fact the startMs rollover masks
//     upstream corruption); a literal `max(0, ...)` keeps the
//     upstream bug observable in the binding.DurationMs value
//     so an operator sees the rollover index hit in the logs.
//   - The downstream caller pattern in
//     scene_planner.go::PlanFromClipEvidence is
//
//     if binding.DurationMs <= 0 {
//         binding.DurationMs = scriptpkg.ClipDurationMsFromAssetID(clipID)
//     }
//
//     which short-circuits on any `<= 0` value. The literal
//     `max(0, ...)` returns 0 in the `endMs <= startMs` branch
//     (reversed offsets), so a malformed upstream payload that
//     surfaces uniform-zero StartMs + EndMs DOES hit the
//     asset-level fallback (which itself returns 0 honestly —
//     the "unknown duration" surface). An upstream rollover
//     (-100, 200) is intentionally NOT masked: a positive
//     DurationMs from a negative startMs is a debugging
//     signal, not a fabricated value.
//
// Returns (literal):
//   - startMs=0  , endMs=1000 → 1000 (canonical positive segment)
//   - startMs=500, endMs=2000 → 1500 (mid-clip segment)
//   - startMs=500, endMs=200  → 0   (reversed / invalid)
//   - startMs=0  , endMs=0    → 0   (no segment info)
//   - startMs=1000,endMs=1000 → 0   (equal — no time elapsed)
//   - startMs=999, endMs=1000 → 1   (near-zero segment)
//   - startMs=-100,endMs=200  → 300 (negative startMs NOT clamped —
//                                   operator sees the rollover signal)
//
// godlike/06 round-trip: the math matches the canonical
// `ClipMetadata / NarrativeClipView DurationMs` derivation in
// clip_source_evidence.go::buildModelClipView verbatim (a
// reverse-coupled re-derivation is filed separately). Keeping
// the math identical at both sites lets the round-trip
// `source_spec_planner_roundtrip_test` assert byte-for-byte
// equality across the planner + planner-input boundary.
//
// godlike/07 fail-fast: the function is pure (no I/O, no
// ports); every caller-visible failure mode is captured in
// the math. It cannot fail.
func ClipDurationMs(startMs, endMs int64) int64 {
	if endMs > startMs {
		return endMs - startMs
	}
	return 0
}

// ClipDurationMsFromAssetID is the canonical "honest null"
// surface for the asset-level full-clip duration lookup. By
// contract this function returns 0 — the script package has no
// asset-access port (godlike/06 layering: domain/script is the
// bottom of the import graph; importing media/processor,
// qdrant, or any other infrastructure layer here would break
// the layering invariant).
//
// Callers that need the actual asset-level duration route
// through the composition-root adapter (AssetDurationResolver
// port, decomposition plan filed in the FASE-2 godlike/06 SSOT
// note on `ClipBinding.DurationMs` in model_output.go);
// reconstructing the asset-level lookup here would violate
// godlike/07 NO-FAKE-AVAILABILITY (a fake-availability
// capability the binding layer does not own).
//
// The assetID parameter is preserved on the signature so
// future SSOT-consistent evolution (a true composition-port
// wired to the asset ID lookup) can replace this function
// without breaking call sites; today the parameter is
// accepted and ignored.
//
// godlike/07 fail-closed: the absence of an asset lookup
// returns 0 (the "unknown duration" surface) rather than a
// sentinel error. The caller pattern in
// internal/application/scripts/scene/scene_planner.go:PlanFromClipEvidence
// is
//
//	if binding.DurationMs <= 0 {
//	    binding.DurationMs = scriptpkg.ClipDurationMsFromAssetID(clipID)
//	}
//
// which only fires when the segment math yields zero (the
// `<= 0` guard short-circuits anything-positive). The
// caller-side policy stays in the binder / planner where
// the related concerns live; returning an error here would
// force every caller to plumb a wrapped error to a binding
// field that already documents zero as "unknown duration",
// defeating both godlike/07 (fake availability) and the
// existing caller contract.
//
// godlike/06 SSOT: this function IS the single canonical
// "unknown asset-level duration" surface for ClipBinding.
// Adding any other "return 0 when asset-level lookup is
// unavailable" branch elsewhere in the codebase would
// silently drift the semantic.
func ClipDurationMsFromAssetID(_ string) int64 {
	return 0
}
