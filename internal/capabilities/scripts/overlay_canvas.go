// Package scriptgeneration — overlay_canvas.go owns the render-canvas value
// object and its style-projection helpers for the semantic OverlayPlan
// derivation. It is the cohesive canvas/style sibling of overlay_plan.go:
// the canvas is the run-level render context (dimensions, background,
// typography, motion pools) that CompileOverlayPlan receives, and the
// helpers project OverlayStyleSpec into the plan's param map.
//
// Split from overlay_plan.go (625 → <600) to satisfy the strict 600-LOC
// forward-prevention gate (godlike/08) without changing behaviour. The two
// files share package scriptgeneration and no new package is introduced.
package scriptgeneration

import (
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/overlay"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// heavyPhrasePriorityDefault is the score band above which a generated phrase
// counts as HEAVY for the E2 motion mapping. The admitted-phrase priority is
// the annotation score the semantic profile grounded the phrase with, so a
// phrase at or above this value gets the prominent entrance and everything
// below keeps the calm rotation.
const heavyPhrasePriorityDefault = 0.85

// OverlayCanvasSpec is the target render canvas for the derived OverlayPlan.
// The runner's withDefaults() resolves a zero spec to the production contract
// (1920×1080 @ 24/1), matching the AssemblyReadyVideoContract.
// Golden/certification tests use GoldenOverlayCanvas (1280×720 @ 30/1) explicitly.
type OverlayCanvasSpec struct {
	Width                  int
	Height                 int
	FPSNum                 int
	FPSDen                 int
	ForegroundScalePercent int
	Background             *capabilityoverlay.OverlayBackground
	Style                  *scriptpkg.OverlayStyleSpec
	// PhraseMotions is the run's phrase-motion rotation pool, carried from
	// the request's channel profile (empty = the certified default pool). It
	// lives here because the canvas is the run-level render context this
	// function already receives; the planner validates the ids fail-closed.
	PhraseMotions      []string
	PhraseMotionFamily string
	ImageMotions       []string
	// HeavyPhrasePriority splits the phrase lane by editorial weight (goal E2):
	// phrases at or above this priority get a prominent certified entrance, the
	// rest keep the calm rotation. Zero disables the split. The runner fills in
	// the production default when the request carries none.
	HeavyPhrasePriority float64
	// MaxPhraseOverlays overrides the run-level grounded-phrase ceiling for
	// this render. It is caller-supplied (request max_phrase_overlays); zero
	// keeps the certified default (capabilityoverlay.MaxPhraseOverlaysPerRun).
	MaxPhraseOverlays int
	// MaxMapOverlays is the caller-selected run-level cap; zero keeps the
	// certified default.
	MaxMapOverlays int
	MapsOnly       bool
	// MaxImageOverlays overrides the run-level image ceiling; zero keeps the
	// certified default (capabilityoverlay.MaxImageOverlaysPerRun).
	MaxImageOverlays int
	AnimationCounts  map[string]int
	// EntityStyleID pins the entity-card composition family for generated
	// entity cards (channel profile or explicit job choice). Empty keeps the
	// certified "random" selector; the planner validates fail-closed.
	EntityStyleID         string
	DisableNumberOverlays bool
}

// GoldenOverlayCanvas is the validated golden canary canvas (1280×720,
// 30/1 FPS, 5 seconds of job) — the same canvas the cross-repo canary renders.
// Production runners derive from the AssemblyReadyVideoContract (1920×1080 @ 24/1).
var GoldenOverlayCanvas = OverlayCanvasSpec{Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1}

func (c OverlayCanvasSpec) withDefaults() OverlayCanvasSpec {
	if c.Width <= 0 || c.Height <= 0 || c.FPSNum <= 0 || c.FPSDen <= 0 {
		// Production default: derived from the AssemblyReadyVideoContract
		// (1920×1080 @ 24/1). Golden/certification paths use GoldenOverlayCanvas
		// explicitly. Preserve all caller-owned semantic fields: the old
		// replacement silently dropped Background and Style whenever the
		// production canvas dimensions were left at zero.
		c.Width, c.Height, c.FPSNum, c.FPSDen = 1920, 1080, 24, 1
	}
	return c
}

func overlayBackgroundFromPayload(src *scriptpkg.OverlayBackgroundSpec) *capabilityoverlay.OverlayBackground {
	return scriptoverlay.BackgroundFromPayload(src)
}

func overlayStyleParams(style *scriptpkg.OverlayStyleSpec) map[string]any {
	return scriptoverlay.StyleParams(style)
}

func overlayImageFrame(style *scriptpkg.OverlayImageStyleSpec) *capabilityoverlay.OverlayItemFrame {
	return scriptoverlay.ImageFrame(style)
}

func isImageOverlayItem(item capabilityoverlay.OverlayItem) bool {
	return scriptoverlay.IsImageOverlayItem(item)
}

func applyOverlayImageStyle(item *capabilityoverlay.OverlayItem, style *scriptpkg.OverlayImageStyleSpec) {
	scriptoverlay.ApplyImageStyle(item, style)
}

func normalizeEntityImageLayer(item *capabilityoverlay.OverlayItem) {
	scriptoverlay.NormalizeEntityImageLayer(item)
}

func isRuntimeTextStyleParam(key string) bool {
	return scriptoverlay.IsRuntimeTextStyleParam(key)
}

func isTextOverlayKind(kind string) bool {
	return scriptoverlay.IsTextOverlayKind(kind)
}

func isEntityOverlayKind(kind string) bool {
	return scriptoverlay.IsEntityOverlayKind(kind)
}

func mergeStyleParam(existing any, add map[string]any) map[string]any {
	return scriptoverlay.MergeStyleParam(existing, add)
}
