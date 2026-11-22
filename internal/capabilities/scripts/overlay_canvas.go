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
	"fmt"
	"strings"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
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
	Background             *capmodel.OverlayBackground
	Style                  *scriptpkg.OverlayStyleSpec
	// OutputProfileID is the RenderingGen certification profile that will
	// describe the rendered artifact, and therefore the ONE input the sealed
	// plan derives its media contract from (OverlayPlan.StampOutputProfile).
	// It lives with the other output-shape facts because both are properties
	// of the artifact, not of the editorial content: a final-job composition is
	// opaque H.264 without audio, while an intermediate overlay is the
	// alpha-bearing ProRes the contract defaults to. Empty keeps the alpha
	// default.
	OutputProfileID string
	// ShortPhraseMotion pins one certified motion for every 1-5-word phrase.
	// Empty keeps the registry's per-word buckets.
	ShortPhraseMotion string
	// ShortPhraseMotionsByWord is the certified short-phrase pool keyed by word
	// count ("1".."5"). Empty (the default) lets the request builder fill it
	// from the animation registry, so the plan always carries the authority's
	// buckets rather than a second hand-maintained list.
	ShortPhraseMotionsByWord map[string][]string
	// LongPhraseMotions is the certified long-phrase (6+ words) pool. Empty
	// (the default) lets the request builder fill it from the registry.
	LongPhraseMotions  []string
	PhraseMotionFamily string
	DateMotionFamily   string
	// EditorialBulletStyles pins the font and entrance motion of individual
	// projected editorial bullets, keyed by scene. Empty keeps the
	// presentation defaults. The canvas carries it because the pin is a
	// run-level render choice this function already receives; the overlay
	// compiler applies it after the presentation pass.
	EditorialBulletStyles []scriptpkg.EditorialBulletStyle
	// MotionSeed is the per-render entropy for automatic motion rotation,
	// carried from the request. Empty preserves historical plan-identity
	// sampling; a fresh value per render rotates every lane.
	MotionSeed   string
	ImageMotions []string
	// ImageWithTextMotions is the run-level pool for captioned images; empty
	// keeps the certified full seeded catalog.
	ImageWithTextMotions []string
	EntityImageMotions   []string
	// EntityCaptionMotions is the run-level caption pool carried from the
	// request (empty = the certified automatic pool). It lives here because
	// the canvas is the run-level render context this function already
	// receives; assignment validates the ids fail-closed.
	EntityCaptionMotions []string
	// AnimationDirectives è il contratto canonico v2 (P2-1): tipizzato per
	// scena/entità/elemento, con mode auto/prefer/exact.
	AnimationDirectives []scriptpkg.AnimationDirective
	// AnimationContractVersion must be >= 2 when directives are present.
	AnimationContractVersion int
	// AnimationCatalogDigest pinna la vista capacità runtime (P0-3).
	AnimationCatalogDigest        string
	RandomLightLeakOnClipStart    bool
	RandomLightLeakOnOverlayStart bool
	RandomImageEffects            bool

	MapType            string
	MapCameraAnimation string
	MapLabelAnimation  string
	MapAnimation       string
	MapLevel           string
	// MapPresentation is the RESOLVED unified map configuration; when set it
	// also owns the map-grouping policy fed to the planner.
	MapPresentation *scriptpkg.MapPresentation
	// HeavyPhrasePriority splits the phrase lane by editorial weight (goal E2):
	// phrases at or above this priority get a prominent certified entrance, the
	// rest keep the calm rotation. Zero disables the split. The runner fills in
	// the production default when the request carries none.
	HeavyPhrasePriority float64
	// MaxPhraseOverlays overrides the run-level grounded-phrase ceiling for
	// this render. It is caller-supplied (request max_phrase_overlays); zero
	// keeps the certified default (capabilityoverlay.MaxPhraseOverlaysPerRun).
	MaxPhraseOverlays   int
	MaxOverlaysPerScene int
	// MaxMapOverlays is the caller-selected run-level cap; zero keeps the
	// certified default.
	MaxMapOverlays int
	MapsOnly       bool
	// MaxImageOverlays overrides the run-level image ceiling; zero keeps the
	// certified default (capabilityoverlay.MaxImageOverlaysPerRun).
	MaxImageOverlays int
	// EditorialDensity is the SINGLE editorial budget control: minimal,
	// balanced (default) or dynamic. It derives the caps only where the caller
	// left them unset, so the historical explicit caps remain precise user
	// overrides. Unknown values fail closed at plan compilation.
	EditorialDensity string
	// RequireEntityImages makes the entity-image expectation originate in the
	// request policy, even when no entity_image overlay was successfully built.
	RequireEntityImages bool
	// StrictEntityImageRequirement is the request-level opt-in for rejecting
	// text-only identity cards. It is separate from RequireEntityImages, which
	// also arms the ordinary image preflight for enabled image lanes.
	StrictEntityImageRequirement bool
	EntityImagesRequireDrive     bool
	RequiredEntityTypes          []string
	// OverlayGroupLimits carries the per-group caps (see overlay_group_limits).
	OverlayGroupLimits      map[string]int
	AnimationCounts         map[string]int
	OverlayCategories       map[scriptpkg.OverlayCategory]scriptpkg.Toggle
	OverlayCategorySegments []scriptpkg.ScriptSegment
	// EntityStyleID pins the entity-card composition family for generated
	// entity cards (channel profile or explicit job choice). Empty keeps the
	// certified "random" selector; the planner validates fail-closed.
	EntityStyleID         string
	EntityBadgeColor      string
	DisableNumberOverlays bool
	// DisableRecapOverlays is the kill-switch for automatic recap cards
	// (recap_plan.go). False by default: recaps ride the certified quote
	// primitive at chapter boundaries unless explicitly disabled.
	DisableRecapOverlays bool
	// DisableChartOverlays is the kill-switch for automatic bar charts
	// (chart_plan.go). False by default.
	DisableChartOverlays bool
	// DisableEditorialOverlays skips the automatic scene_title/scene_bullet
	// projection so operator-supplied grounded phrases keep the run budget
	// (catalog runs). False by default.
	DisableEditorialOverlays bool
	// EnableCharts is the explicit opt-in for chart overlays. Charts render
	// only when this is true and DisableChartOverlays is false.
	EnableCharts bool
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
		c.Width, c.Height, c.FPSNum, c.FPSDen = kernelmedia.CanonicalWidth, kernelmedia.CanonicalHeight, kernelmedia.CanonicalFPSNum, kernelmedia.CanonicalFPSDen
	}
	return c.applyEditorialDensity()
}

const (
	editorialDensityMinimal  = "minimal"
	editorialDensityBalanced = "balanced"
	editorialDensityDynamic  = "dynamic"
)

// validateOverlayEditorialDensity fails closed on an unknown preset. It is
// called by both the v2 request builder and the legacy fixture projection so a
// typo never silently falls back to the balanced defaults.
func validateOverlayEditorialDensity(c OverlayCanvasSpec) error {
	switch normalizeEditorialDensity(c.EditorialDensity) {
	case "", editorialDensityMinimal, editorialDensityBalanced, editorialDensityDynamic:
		return nil
	default:
		return fmt.Errorf("overlay plan: editorial_density %q must be minimal, balanced or dynamic", c.EditorialDensity)
	}
}

func normalizeEditorialDensity(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// applyEditorialDensity lowers the operator-facing preset into the four
// editorial ceilings. Explicit per-run caps win: the preset only fills zeros.
// The phrase cap is always clamped to the certified hard limit.
func (c OverlayCanvasSpec) applyEditorialDensity() OverlayCanvasSpec {
	switch normalizeEditorialDensity(c.EditorialDensity) {
	case editorialDensityMinimal:
		if c.MaxPhraseOverlays <= 0 {
			c.MaxPhraseOverlays = 3
		}
		if c.MaxImageOverlays <= 0 {
			c.MaxImageOverlays = 4
		}
		if c.MaxMapOverlays <= 0 {
			c.MaxMapOverlays = 1
		}
		if c.MaxOverlaysPerScene <= 0 {
			c.MaxOverlaysPerScene = 1
		}
	case editorialDensityDynamic:
		if c.MaxPhraseOverlays <= 0 {
			c.MaxPhraseOverlays = 10
		}
		if c.MaxImageOverlays <= 0 {
			c.MaxImageOverlays = 12
		}
		if c.MaxMapOverlays <= 0 {
			c.MaxMapOverlays = 2
		}
		if c.MaxOverlaysPerScene <= 0 {
			c.MaxOverlaysPerScene = 3
		}
	}
	if c.MaxPhraseOverlays > capabilityoverlay.MaxPhraseOverlaysHardLimit {
		c.MaxPhraseOverlays = capabilityoverlay.MaxPhraseOverlaysHardLimit
	}
	return c
}

func overlayBackgroundFromPayload(src *scriptpkg.OverlayBackgroundSpec) *capmodel.OverlayBackground {
	return capabilityoverlay.BackgroundFromPayload(src)
}

func effectiveImageStyle(style *scriptpkg.OverlayImageStyleSpec) *scriptpkg.OverlayImageStyleSpec {
	effective := &scriptpkg.OverlayImageStyleSpec{
		// Plain photo overlays render full-bleed: a default border covers
		// edge pixels and a default radius crops corners, so both default
		// to zero. Explicit payload styles still win via the merge below.
		Border: &scriptpkg.OverlayImageBorder{Width: 0, Color: "#111827", Radius: float64Pointer(0)},
		Shadow: &scriptpkg.OverlayShadowSpec{Enabled: false, Color: "#000000", Opacity: float64Pointer(0),
			Blur: float64Pointer(0), Offset: []float64{0, 0}},
	}
	if style == nil {
		return effective
	}
	copy := *style
	if copy.Border == nil {
		copy.Border = effective.Border
	}
	if copy.Shadow == nil {
		copy.Shadow = effective.Shadow
	}
	return &copy
}

func float64Pointer(value float64) *float64 { return &value }

func isImageOverlayItem(item capmodel.OverlayItem) bool {
	return capabilityoverlay.IsAssetImageOverlayItem(item)
}

func applyOverlayImageStyle(item *capmodel.OverlayItem, style *scriptpkg.OverlayImageStyleSpec) {
	capabilityoverlay.ApplyImageStyle(item, effectiveImageStyle(style))
}

// ── Overlay category filter (merged from overlay_category_filter.go) ──
//
// Merged here to keep the registered scripts hotspot at its committed
// baseline: the filter is canvas policy (it reads OverlayCanvasSpec) and
// this file is its cohesive home. No behaviour change.

// dropCategoryDisabledCandidates removes a scene's candidates for every
// disabled overlay lane BEFORE the planner runs. The run-level budgets dedupe
// globally, so a disabled scene's identical date, phrase or image surface
// would otherwise evict the enabled scene's copy before the end category
// filter can remove the disabled one. Quotes and keywords pass through: the
// internal editorial budget never retains them, so they cannot evict anything.
// Numbers survive when charts are enabled because chart placements derive from
// grounded numbers, while the number cards themselves still die at the end
// filter.

// Merged here to keep the registered scripts hotspot at its committed
// baseline: group capping reads the canvas OverlayGroupLimits and the
// canvas helpers above. No behaviour change.

// Overlay group keys accepted by the group-limit validation.
const (
	overlayGroupMaps             = "maps"
	overlayGroupShortPhrases     = "short_phrases"
	overlayGroupLongPhrases      = "long_phrases"
	overlayGroupEntitiesWithText = "entities_with_text"
	overlayGroupLocations        = "locations"
	overlayGroupImages           = "images"
	overlayGroupBullets          = "bullets"
	// overlayGroupDates caps the number/date card lane. Date cards are easy to
	// extract and otherwise crowd out the entity/image lanes an editorial run
	// actually asked for. The group is opt-in: an absent key preserves the
	// historical per-scene MaxNumbers behavior.
	overlayGroupDates = "dates"
)

var overlayGroupKeys = map[string]bool{
	overlayGroupMaps:             true,
	overlayGroupShortPhrases:     true,
	overlayGroupLongPhrases:      true,
	overlayGroupEntitiesWithText: true,
	overlayGroupLocations:        true,
	overlayGroupImages:           true,
	overlayGroupBullets:          true,
	overlayGroupDates:            true,
}

func cloneOverlayGroupLimits(src map[string]int) map[string]int {
	if src == nil {
		return nil
	}
	dst := make(map[string]int, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

// validateOverlayGroupLimits fails closed on unknown keys or negative caps.

// overlayImageLayersHaveCaptions reports whether a composite carries any
// per-layer caption text. Composite parents clear EntityCaption to avoid
// duplicating the first name, so the group classifier must look at layers.

// overlayGroupOf assigns one overlay to exactly one group. Maps, images,
// entity cards and phrases are disjoint; short phrases use the exact-cardinality
// short recipes, every other grounded phrase counts as long.

// applyOverlayGroupLimits keeps, per group, at most the configured number of
// overlays. Present zero drops the group; absent keys and ungrouped items pass
// through unchanged.
//
// A capped group spends its allowance across SCENES before taking a second
// overlay from any one scene. Counting the candidates in plan order starves
// every scene past the allowance, because the plan carries both cards of the
// first scenes before the first card of the last one: a fifteen-scene run with
// an entities_with_text cap of fifteen rendered ten name cards in ten scenes
// and left five scenes empty. The kept set is therefore chosen per group —
// the first overlay of every scene in plan order, then the ranked remainder —
// while the emitted items still follow plan order, so the render plan's
// z-order and content identity are unchanged.

// overlayGroupLimit returns the cap for a group and whether it was set.
// applyPhraseLaneCaps forwards explicit short/long phrase group caps into the
// planner's first editorial budget pass. BuildPlan budgets before the later
// materialization pass, so without these caps its default long-first selector
// can discard short candidates before the split-lane budget admits them.

// effectivePhraseOverlayCeiling combines the explicit run-level phrase limit
// with any phrase-group caps. A single configured group is enough to raise the
// planner ceiling; the other group is left uncapped (not treated as zero).

// effectiveMapOverlayCeiling combines the request and group caps under the
// hard run limit. Explicit ceilings are honored exactly — the smaller of the
// request and the maps group cap wins — while oversized requests are bounded
// by MaxMapOverlaysPerRun so payloads cannot create unbounded basemap
// renders. Zero keeps the certified run ceiling.
