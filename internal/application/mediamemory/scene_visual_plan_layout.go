// Package mediamemory — scene_visual_plan_layout.go is the canonical
// SSOT for the SceneVisualPlan layer-layout vocabulary (Fase 4.2).
//
// godlike/06 SSOT (one canonical owner per fact): this file is the
// SINGLE owner of the layer layout string vocabulary. The renderer
// (internal/infrastructure/media/render/cutter.go + transitions.go)
// reads `Layer.Layout` as one of these values; drift surfaces as
// godlike/07 NO-FAKE-AVAILABILITY at the renderer's predicate
// (`IsKnownLayout`) so a stale binding never silently degrades to
// the renderer's default-fallback path.
//
// godlike/06 SSOT (closed set): the layout set is exhaustively
// enumerated below. A future agent that needs a new layout MUST
// add it here + bump SchemaVersion in scene_visual_plan_dto.go.
// Anything outside this set is rejected at the generator
// boundary so the ranker's wire envelope stays clean.
package mediamemory

// LayoutKind is the canonical closed-set of layer layouts.
// The renderer reads this verbatim as `Layer.Layout`.
type LayoutKind string

const (
	// LayoutFullscreen is the canonical layout for primary_video:
	// the layer fills the entire frame, no inset.
	LayoutFullscreen LayoutKind = "fullscreen"
	// LayoutFullscreenFade is the canonical layout for
	// evidence_overlay: fullscreen with a fade-in/fade-out
	// transition envelope.
	LayoutFullscreenFade LayoutKind = "fullscreen_fade"
	// LayoutRightPanel is the canonical layout for
	// secondary_image: the layer occupies the right portion
	// of the frame (typically 30-40% width) so the primary
	// clip stays visible behind it.
	LayoutRightPanel LayoutKind = "right_panel"
	// LayoutLowerThird is a forward-pinned layout (typically
	// used for document slots or graphics overlays).
	LayoutLowerThird LayoutKind = "lower_third"
	// LayoutSplitScreen is the canonical 50/50 layout for
	// two-slot scenes where neither layer dominates.
	LayoutSplitScreen LayoutKind = "split_screen"
	// LayoutPictureInPicture is a forward-pinned layout for
	// nested video insets (e.g. presenter over B-roll).
	LayoutPictureInPicture LayoutKind = "picture_in_picture"
)

// IsKnownLayout reports whether k is in the canonical closed set.
// The renderer gates every Layer.Layout read on this predicate so
// drift surfaces as godlike/07 NO-FAKE-AVAILABILITY.
func IsKnownLayout(k LayoutKind) bool {
	switch k {
	case LayoutFullscreen, LayoutFullscreenFade, LayoutRightPanel,
		LayoutLowerThird, LayoutSplitScreen, LayoutPictureInPicture:
		return true
	default:
		return false
	}
}

// DefaultLayoutForSlot is the canonical mapping from the canonical
// scene-slot triple to the canonical default layout. godlike/06
// SSOT (slot ↔ layout SSOT): every slot has EXACTLY ONE canonical
// default — the renderer reads this verbatim. Per-binding
// overrides are a forward-pin.
//
//	default           SlotKind            LayoutKind
//	primary_video     SlotPrimaryVideo    LayoutFullscreen
//	secondary_image   SlotSecondaryImage  LayoutRightPanel
//	evidence_overlay  SlotEvidenceOverlay LayoutFullscreenFade
//	map               SlotMap             LayoutFullscreenFade
//	portrait          SlotPortrait        LayoutRightPanel
//	document          SlotDocument        LayoutLowerThird
//	background        SlotBackground      LayoutFullscreenFade
//
// Unknown SlotKind values fall through to LayoutFullscreenFade
// (the safest default per the renderer's fallback path) and the
// generator surfaces a Warning via PlanWithWarnings so an operator
// can audit the drift.
func DefaultLayoutForSlot(s SlotKind) LayoutKind {
	switch s {
	case SlotPrimaryVideo:
		return LayoutFullscreen
	case SlotSecondaryImage, SlotPortrait:
		return LayoutRightPanel
	case SlotEvidenceOverlay, SlotMap, SlotBackground:
		return LayoutFullscreenFade
	case SlotDocument:
		return LayoutLowerThird
	default:
		// godlike/07 NO-FAKE-AVAILABILITY: an unknown slot kind
		// never silently zero-outputs; the renderer can still
		// play the resulting layer with the fade default, but
		// the diagnostic is on the Warnings[] slice.
		return LayoutFullscreenFade
	}
}

// CanonicalStartEndFraction returns the canonical (startFrac,
// endFrac) frame-window fractions for a slot within a scene
// [0..scene.DurationMs]. godlike/06 SSOT (deterministic fitting):
// each slot has a fixed canonical window so the renderer reads
// every Layer as if its StartMs/EndMs were SSOT.
//
// primary_video       → [0.00, 1.00]      (full scene)
// secondary_image     → [0.60, 0.95]     (inset toward the end)
// evidence_overlay    → [0.40, 0.95]     (mid-to-late background)
// map                 → [0.40, 0.95]     (overlay slot)
// portrait            → [0.60, 0.95]     (same as secondary_image)
// document            → [0.65, 0.92]     (lower-third)
// background          → [0.40, 0.95]     (same as evidence_overlay)
//
// The values are stored as fractions of scene.DurationMs so a
// future per-project override can re-derive StartMs/EndMs without
// touching this SSOT.
func CanonicalStartEndFraction(s SlotKind) (startFrac, endFrac float64) {
	switch s {
	case SlotPrimaryVideo:
		return 0.00, 1.00
	case SlotSecondaryImage, SlotPortrait:
		return 0.60, 0.95
	case SlotEvidenceOverlay, SlotMap, SlotBackground:
		return 0.40, 0.95
	case SlotDocument:
		return 0.65, 0.92
	default:
		return 0.00, 1.00
	}
}

// FitLayerWindow projects a binding's canonical
// [StartMs, EndMs] onto a [0, sceneDurationMs] window using the
// godlike/06 SSOT (binding window first, scene-fraction fallback):
//
//  1. If binding.StartMs == 0 AND binding.EndMs == 0 (the
//     canonical "no sub-clip" sentinel for image / document
//     bindings): use the slot's CanonicalStartEndFraction.
//  2. If the binding's window is fully within [0, sceneDurationMs]:
//     use it verbatim.
//  3. Otherwise (binding window exceeds scene duration, common
//     for video bindings attached to a short scene): CLAMP to
//     the scene duration. godlike/06 SSOT (canonical clamp
//     rule) — the renderer cuts the bytes per the canonical
//     window so out-of-window bytes don't leak into the next
//     scene.
func FitLayerWindow(slot SlotKind, bindStartMs, bindEndMs, sceneDurationMs int64) (int64, int64) {
	if sceneDurationMs <= 0 {
		sceneDurationMs = 1
	}
	// No binding window: use slot canonical fractions.
	if bindStartMs == 0 && bindEndMs == 0 {
		sf, ef := CanonicalStartEndFraction(slot)
		return int64(float64(sceneDurationMs) * sf), int64(float64(sceneDurationMs) * ef)
	}
	// Binding window is already within [0, scene]: pass through.
	if bindStartMs >= 0 && bindEndMs <= sceneDurationMs && bindEndMs > bindStartMs {
		return bindStartMs, bindEndMs
	}
	// Binding window exceeds scene: clamp to scene duration.
	if bindEndMs > sceneDurationMs {
		return bindStartMs, sceneDurationMs
	}
	if bindStartMs < 0 {
		start, _ := CanonicalStartEndFraction(slot)
		return int64(float64(sceneDurationMs) * start), bindEndMs
	}
	// Degenerate binding window (negative duration): fallback.
	sf, ef := CanonicalStartEndFraction(slot)
	return int64(float64(sceneDurationMs) * sf), int64(float64(sceneDurationMs) * ef)
}
