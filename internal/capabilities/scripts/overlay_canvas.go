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
	// MotionSeed is the per-render entropy for automatic motion rotation,
	// carried from the request. Empty preserves historical plan-identity
	// sampling; a fresh value per render rotates every lane.
	MotionSeed string
	// CoherenceSeed carries the explicit dominant-direction seed from the
	// request; empty derives the direction from MotionSeed.
	CoherenceSeed      string
	ImageMotions       []string
	EntityImageMotions []string
	// EntityCaptionMotions is the run-level caption pool carried from the
	// request (empty = the certified automatic pool). It lives here because
	// the canvas is the run-level render context this function already
	// receives; assignment validates the ids fail-closed.
	EntityCaptionMotions []string
	AnimationPolicies    []scriptpkg.AnimationPolicy
	// AnimationDirectives è il contratto canonico v2 (P2-1): tipizzato per
	// scena/entità/elemento, con mode auto/prefer/exact. I vecchi campi
	// restano ingressi compatibili normalizzati una sola volta.
	AnimationDirectives []scriptpkg.AnimationDirective
	// AnimationContractVersion: 0/1 legacy dual-read, 2 canonico (P2-6).
	AnimationContractVersion int
	// AnimationCatalogDigest pinna la vista capacità runtime (P0-3).
	AnimationCatalogDigest        string
	RandomLightLeakOnClipStart    bool
	RandomLightLeakOnOverlayStart bool

	MapType            string
	MapCameraAnimation string
	MapLabelAnimation  string
	MapAnimation       string
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
	// OverlayGroupLimits carries the per-group caps (see overlay_group_limits).
	OverlayGroupLimits      map[string]int
	AnimationCounts         map[string]int
	OverlayCategories       map[scriptpkg.OverlayCategory]scriptpkg.Toggle
	OverlayCategorySegments []scriptpkg.ScriptSegment
	// EntityStyleID pins the entity-card composition family for generated
	// entity cards (channel profile or explicit job choice). Empty keeps the
	// certified "random" selector; the planner validates fail-closed.
	EntityStyleID         string
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
	return scriptoverlay.ImageFrame(effectiveImageStyle(style))
}

func effectiveImageStyle(style *scriptpkg.OverlayImageStyleSpec) *scriptpkg.OverlayImageStyleSpec {
	effective := &scriptpkg.OverlayImageStyleSpec{
		// Plain photo overlays render full-bleed: a default border covers
		// edge pixels and a default radius crops corners, so both default
		// to zero. Explicit payload styles still win via the merge below.
		Border: &scriptpkg.OverlayImageBorder{Width: 0, Color: "#111827", Radius: float64Pointer(0)},
		Shadow: &scriptpkg.OverlayShadowSpec{Enabled: true, Color: "#000000", Opacity: float64Pointer(0.7),
			Blur: float64Pointer(10), Offset: []float64{0, 4}},
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

func isImageOverlayItem(item capabilityoverlay.OverlayItem) bool {
	return scriptoverlay.IsImageOverlayItem(item)
}

func isImageOverlayKind(kind string) bool {
	return scriptoverlay.IsImageOverlayKind(kind)
}

func imageStyleParams(style *scriptpkg.OverlayImageStyleSpec) map[string]any {
	return scriptoverlay.ImageStyleParams(effectiveImageStyle(style))
}

func applyOverlayImageStyle(item *capabilityoverlay.OverlayItem, style *scriptpkg.OverlayImageStyleSpec) {
	scriptoverlay.ApplyImageStyle(item, effectiveImageStyle(style))
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

// ── Overlay category filter (merged from overlay_category_filter.go) ──
//
// Merged here to keep the registered scripts hotspot at its committed
// baseline: the filter is canvas policy (it reads OverlayCanvasSpec) and
// this file is its cohesive home. No behaviour change.

func hasSegmentOverlayCategories(segments []scriptpkg.ScriptSegment) bool {
	for _, segment := range segments {
		if len(segment.OverlayCategories) > 0 {
			return true
		}
	}
	return false
}

func filterOverlayItemsByCategory(items []capabilityoverlay.OverlayItem, scenes []Scene, canvas OverlayCanvasSpec) []capabilityoverlay.OverlayItem {
	if len(canvas.OverlayCategories) == 0 && !hasSegmentOverlayCategories(canvas.OverlayCategorySegments) {
		return items
	}
	indexes := make(map[string]int, len(scenes))
	for _, scene := range scenes {
		indexes[scene.ID] = scene.Index
	}
	out := make([]capabilityoverlay.OverlayItem, 0, len(items))
	for _, item := range items {
		category, ok := overlayItemCategory(item)
		if !ok || overlayCategoryEnabled(category, item.SceneID, indexes[item.SceneID], canvas) {
			out = append(out, item)
		}
	}
	return out
}

func overlayCategoryEnabled(category scriptpkg.OverlayCategory, sceneID string, sceneIndex int, canvas OverlayCanvasSpec) bool {
	value := canvas.OverlayCategories[category]
	for i, segment := range canvas.OverlayCategorySegments {
		id := strings.TrimSpace(segment.ID)
		if id != "" && id == sceneID {
			if override := segment.OverlayCategories[category]; override != "" && override != scriptpkg.ToggleDefault {
				value = override
			}
			break
		}
		if i == sceneIndex && id == "" {
			if override := segment.OverlayCategories[category]; override != "" && override != scriptpkg.ToggleDefault {
				value = override
			}
		}
	}
	return value != scriptpkg.ToggleDisabled
}

func overlayItemCategory(item capabilityoverlay.OverlayItem) (scriptpkg.OverlayCategory, bool) {
	kind := strings.ToLower(strings.TrimSpace(item.Kind))
	template := strings.ToLower(strings.TrimSpace(item.TemplateID))
	role, _ := item.Params["editorial_role"].(string)
	role = strings.ToLower(strings.TrimSpace(role))
	switch {
	case strings.Contains(template, "date") || strings.Contains(template, "metric") || kind == "number":
		return scriptpkg.OverlayCategoryNumbers, true
	case item.Map != nil || kind == "map":
		return scriptpkg.OverlayCategoryMaps, true
	case strings.Contains(template, "chart") || kind == "chart_card" || kind == "chart_bar":
		return scriptpkg.OverlayCategoryCharts, true
	case item.Params["recap"] == true || strings.Contains(template, "recap"):
		return scriptpkg.OverlayCategoryRecaps, true
	// Editorial roles override the shared IMPORTANT_PHRASE template: the
	// request can independently enable bullets while disabling generic
	// phrases/titles, and the template name alone must not recategorize a
	// scene_bullet before its explicit role is examined.
	case role == "bullet" || role == "bullet_point" || role == "scene_bullet":
		return scriptpkg.OverlayCategoryBullets, true
	case role == "title" || role == "headline" || role == "scene_title":
		return scriptpkg.OverlayCategoryTitles, true
	case strings.Contains(template, "title"):
		return scriptpkg.OverlayCategoryTitles, true
	case strings.Contains(template, "bullet"):
		return scriptpkg.OverlayCategoryBullets, true
	case strings.Contains(template, "quote") || role == "quote":
		return scriptpkg.OverlayCategoryQuotes, true
	case strings.Contains(kind, "brand") || role == "brand":
		return scriptpkg.OverlayCategoryBrandText, true
	case kind == "text_phrase" || strings.Contains(template, "phrase"):
		return scriptpkg.OverlayCategoryPhrases, true
	case kind == string(capabilityoverlay.KindEntityCard) && !isImageOverlayItem(item):
		// A text-only named entity card is NOT imagery, whatever entity type it
		// names: the planner caps these cards per scene and the payload controls
		// them through the `entities_with_text` group limit, exactly like the
		// group classifier (overlayGroupOf) already separates them from `images`.
		// Categorizing them as images would let an `images: disabled` toggle — a
		// control for photo overlays — silently delete every place, person and
		// organization name card in the run, and an image-backed card of the
		// SAME entity would still be filtered by that toggle.
		return "", false
	case strings.Contains(kind, "image") || kind == "entity_card" || kind == "product" || kind == "logo":
		return scriptpkg.OverlayCategoryImages, true
	default:
		return "", false
	}
}

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
)

var overlayGroupKeys = map[string]bool{
	overlayGroupMaps:             true,
	overlayGroupShortPhrases:     true,
	overlayGroupLongPhrases:      true,
	overlayGroupEntitiesWithText: true,
	overlayGroupLocations:        true,
	overlayGroupImages:           true,
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
func validateOverlayGroupLimits(limits map[string]int) error {
	for key, value := range limits {
		if !overlayGroupKeys[key] {
			return fmt.Errorf("overlay_group_limits has unsupported group %q", key)
		}
		if value < 0 {
			return fmt.Errorf("overlay_group_limits[%q] must be >= 0, got %d", key, value)
		}
	}
	return nil
}

// overlayImageLayersHaveCaptions reports whether a composite carries any
// per-layer caption text. Composite parents clear EntityCaption to avoid
// duplicating the first name, so the group classifier must look at layers.
func overlayImageLayersHaveCaptions(item capabilityoverlay.OverlayItem) bool {
	for _, layer := range item.ImageLayers {
		if strings.TrimSpace(layer.Caption) != "" {
			return true
		}
	}
	return false
}

// overlayGroupOf assigns one overlay to exactly one group. Maps, images,
// entity cards and phrases are disjoint; short phrases use the exact-cardinality
// short recipes, every other grounded phrase counts as long.
func overlayGroupOf(item capabilityoverlay.OverlayItem) string {
	switch {
	case item.Kind == "map" && item.Map != nil:
		return overlayGroupMaps
	case item.Kind == string(capabilityoverlay.KindLocation):
		return overlayGroupLocations
	case item.Kind == string(capabilityoverlay.KindEntityImage) && (strings.TrimSpace(item.EntityCaption) != "" || overlayImageLayersHaveCaptions(item)):
		return overlayGroupEntitiesWithText
	case entityCardKind(capabilityoverlay.OverlayKind(item.Kind)):
		return overlayGroupEntitiesWithText
	case isImageOverlayItem(item):
		return overlayGroupImages
	case item.Kind == "text_phrase" || item.Kind == string(capabilityoverlay.KindImportantPhrase):
		// Phrase lane is a content property. Visible motion IDs are often
		// shared by both lanes, so using recipe membership here misclassifies
		// every short phrase that received a standard entrance.
		if words := len(strings.Fields(item.Text)); words >= 2 && words <= 5 {
			return overlayGroupShortPhrases
		}
		return overlayGroupLongPhrases
	}
	return ""
}

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
func applyOverlayGroupLimits(items []capabilityoverlay.OverlayItem, limits map[string]int) []capabilityoverlay.OverlayItem {
	if len(limits) == 0 {
		return items
	}
	// Entity images can carry captions inside the image item and its child
	// layers. Those captions are text overlays too, so an explicit zero for
	// entities_with_text must remove their text and motion before composition.
	if limit, capped := limits[overlayGroupEntitiesWithText]; capped && limit == 0 {
		stripOverlayImageCaptions(items)
	}
	candidates := make(map[string][]int, len(limits))
	for index, item := range items {
		group := overlayGroupOf(item)
		if group == "" {
			continue
		}
		if _, capped := limits[group]; !capped {
			continue
		}
		candidates[group] = append(candidates[group], index)
	}
	keep := make(map[int]struct{}, len(items))
	for group, indices := range candidates {
		seenScenes := make(map[string]struct{}, len(indices))
		diverse := make([]int, 0, len(indices))
		remaining := make([]int, 0, len(indices))
		for _, index := range indices {
			sceneID := strings.TrimSpace(items[index].SceneID)
			if sceneID == "" {
				remaining = append(remaining, index)
				continue
			}
			if _, ok := seenScenes[sceneID]; ok {
				remaining = append(remaining, index)
				continue
			}
			seenScenes[sceneID] = struct{}{}
			diverse = append(diverse, index)
		}
		selected := append(diverse, remaining...)
		if limit := limits[group]; len(selected) > limit {
			selected = selected[:limit]
		}
		for _, index := range selected {
			keep[index] = struct{}{}
		}
	}
	out := items[:0]
	for index, item := range items {
		group := overlayGroupOf(item)
		if _, capped := limits[group]; group == "" || !capped {
			out = append(out, item)
			continue
		}
		if _, ok := keep[index]; ok {
			out = append(out, item)
		}
	}
	return out
}

func stripOverlayImageCaptions(items []capabilityoverlay.OverlayItem) {
	for i := range items {
		items[i].EntityCaption = ""
		items[i].CaptionMotionID = ""
		items[i].CaptionMotionParams = nil
		for layer := range items[i].ImageLayers {
			items[i].ImageLayers[layer].Caption = ""
			items[i].ImageLayers[layer].CaptionMotionID = ""
			items[i].ImageLayers[layer].CaptionMotionParams = nil
		}
	}
}

// overlayGroupLimit returns the cap for a group and whether it was set.
func overlayGroupLimit(limits map[string]int, group string) (int, bool) {
	value, ok := limits[group]
	return value, ok
}

// effectivePhraseOverlayCeiling combines the explicit run-level phrase limit
// with any phrase-group caps. A single configured group is enough to raise the
// planner ceiling; the other group is left uncapped (not treated as zero).
func effectivePhraseOverlayCeiling(requested int, limits map[string]int) int {
	ceiling := capabilityoverlay.EffectivePhraseOverlayLimit(requested)
	shortCap, hasShortCap := overlayGroupLimit(limits, overlayGroupShortPhrases)
	longCap, hasLongCap := overlayGroupLimit(limits, overlayGroupLongPhrases)
	if !hasShortCap && !hasLongCap {
		return ceiling
	}
	groupCeiling := 0
	for _, cap := range []int{shortCap, longCap} {
		if cap <= 0 {
			continue
		}
		if groupCeiling >= capabilityoverlay.MaxPhraseOverlaysHardLimit-cap {
			groupCeiling = capabilityoverlay.MaxPhraseOverlaysHardLimit
			break
		}
		groupCeiling += cap
	}
	if groupCeiling > ceiling {
		ceiling = groupCeiling
	}
	return ceiling
}

// effectiveMapOverlayCeiling combines the request and group caps under the
// hard run limit. Maps are scene-synced camera compositions, so up to three
// may be admitted in one run without allowing payloads to create unbounded
// basemap renders.
func effectiveMapOverlayCeiling(requested int, limits map[string]int) int {
	ceiling := capabilityoverlay.MaxMapOverlaysPerRun
	if requested > 0 && requested < ceiling {
		ceiling = requested
	}
	if group, ok := overlayGroupLimit(limits, overlayGroupMaps); ok && group > 0 && group < ceiling {
		ceiling = group
	}
	return ceiling
}
