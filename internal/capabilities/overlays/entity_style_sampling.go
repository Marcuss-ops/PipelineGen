// entity_style_sampling.go owns the PipelineGen side of the RenderingGen
// entity-style contract (overlay-plan.v1 `entity_style_id`): the selector the
// generated plans carry, the certified selector/variant vocabulary the planner
// is allowed to emit, and the helpers that stamp entity items so every runtime
// entity card samples the live Apple-Spatial-style registry.
//
// RenderingGen owns the composition registry itself, the tag queries and the
// badge yellow/red runtime randomization. The pipeline never opens that
// registry: it stamps "random", and RenderingGen's styleHash sampler resolves
// one variant per (plan, video, item) identity, deterministically — so retries
// are stable while separate entities and plans keep rotating through the live
// catalog. The retired side-caption styles are aliased to below styles in the
// renderer; they are no longer live and can never be sampled.
package overlays

import capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"

import "strings"

// IdentityEntityStyleSelector is the certified entity_style_id value stamped
// on GENERATED entity-image overlay items. RenderingGen resolves it as a tag
// query over the COMPLETE 25-style premium registry (no group filter), so
// runtime variety covers every live group — including badges and 3D camera
// moves — while the retired side-caption styles are no longer in the live
// registry. The selector itself stays random: the user-facing contract is a
// varied pool, never one forced composition.
const IdentityEntityStyleSelector = "random"

// IdentityEntityStyleBelowSelector pins generated entity cards whose caption
// is part of the design to the renderer's below-caption pool. The generic
// random selector can sample a side composition (text_left/text_right), which
// moves the name beside the portrait; generated documentary cards always keep
// the caption below it.
const IdentityEntityStyleBelowSelector = "premium_below_random_v1"

// CertifiedEntityStyleSelectors is the closed set of entity_style_id values
// the pipeline is allowed to put on a plan item. Selector words and the 25
// legacy-style reference numbers resolve in RenderingGen; the planner only
// ever stamps IdentityEntityStyleSelector.
var CertifiedEntityStyleSelectors = []string{
	"random",
	"premium_random_v1",
	"testo_sotto",
	"below",
	"premium_below_random_v1",
	"caption_below",
	"badge",
	"badge_random",
	"camera",
	"camera_random",
}

// CertifiedAppleSpatialEntityStyles lists the legacy Apple Spatial entity
// composition IDs still transportable by channel profiles: centered 3D
// entrances 01–05, retired side layouts 06–10 (aliased to below styles in the
// renderer), retired typewriter treatments 11–15, highlighting badges 16–20,
// and 3D camera movements 21–25. The live registry contains only the below,
// badge and camera compositions; the planner emits the random selector above.
var CertifiedAppleSpatialEntityStyles = []string{
	"01_entity_pitch_lift_text_below",
	"02_entity_yaw_flip_text_below",
	"03_entity_pop_bounce_text_below",
	"04_entity_card_swing_text_below",
	"05_entity_depth_float_text_below",
	"06_entity_swipe_text_right",
	"07_entity_orbit_text_left",
	"08_entity_counter_tilt_text_right",
	"09_entity_dolly_settle_text_left",
	"10_entity_parallax_drift_text_right",
	"11_entity_typewriter_classic",
	"12_entity_terminal_cyber",
	"13_entity_typewriter_editorial_gold",
	"14_entity_code_prompt_reveal",
	"15_entity_typewriter_dolly_breath",
	"16_entity_badge_yellow_wipe",
	"17_entity_badge_red_impact",
	"18_entity_badge_cyan_electric",
	"19_entity_badge_orange_amber",
	"20_entity_badge_green_emerald",
	"21_entity_camera_dolly_push",
	"22_entity_camera_orbit_yaw",
	"23_entity_camera_crane_rise",
	"24_entity_camera_dutch_spatial",
	"25_entity_camera_flyby_parallax",
}

// validateEntityStyleSelector fails closed on a selector the generated pool
// does not own. An unprefixed variant id is still transportable for explicit
// editorial plans (planner-owned channel profiles), so validity is a helper,
// not the generated default.
func validateEntityStyleSelector(styleID string) bool {
	if strings.TrimSpace(styleID) == "" {
		return false
	}
	norm := strings.ToLower(strings.TrimSpace(styleID))
	for _, certified := range CertifiedEntityStyleSelectors {
		if certified == norm {
			return true
		}
	}
	for _, style := range CertifiedAppleSpatialEntityStyles {
		if style == norm {
			return true
		}
	}
	return false
}

// IsValidEntityStyleSelector is the exported admission check for every
// producer of the selector: channel profiles validate at LOAD time, the
// planner validates at BUILD time, and both fail closed on a selector this
// build cannot honour (RenderingGen would reject it at compile).
func IsValidEntityStyleSelector(styleID string) bool {
	return validateEntityStyleSelector(styleID)
}

// stampEntityStyle sets the certified generated selector on an entity item
// that carries BOTH the required content (a portrait asset and a name
// caption): RenderingGen refuses entity_style_id on an item without an image
// and entity_caption, so the stamp keeps the same fail-closed precondition.
func stampEntityStyle(item *capmodel.OverlayItem) {
	if item == nil {
		return
	}
	if len(item.AssetRefs) == 0 || strings.TrimSpace(item.EntityCaption) == "" {
		return
	}
	item.EntityStyleID = IdentityEntityStyleSelector
}

// StampEntityStyle is the exported planner seam: it stamps the certified
// generated selector on every entity-image item that satisfies RenderingGen's
// style precondition (portrait + entity caption), leaving text-only and map
// items untouched.
func StampEntityStyle(items []capmodel.OverlayItem) {
	for index := range items {
		stampEntityStyle(&items[index])
	}
}
