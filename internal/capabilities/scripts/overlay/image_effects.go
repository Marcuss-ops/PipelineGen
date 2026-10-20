// Package overlay — image_effects.go owns the random runtime image effect
// decoration. Each image overlay gets a deterministic yes/no draw; when the
// draw says yes, one EffectsPack-derived light effect (ChrononTemplate's
// twenty procedural looks, mapped to native wire effects in
// capabilityoverlay's decoration table) is attached as the item's
// `params.effects` stack and lowers through RenderingGen onto the photo
// surface. Everything is derived from plan + item identity, so retries of the
// same job replay the same decorations while different images and jobs vary —
// the same contract as the light-leak derivation beside this file.
package overlay

import (
	"encoding/binary"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// AttachRandomImageEffects decorates image overlays in place with the runtime
// effect stack. An item that already declares params.effects is left alone:
// explicit authoring always wins over the random policy, and the check runs
// before the draw so a decorated plan is stable under re-derivation.
func AttachRandomImageEffects(planID string, items []capabilityoverlay.OverlayItem) {
	for index := range items {
		item := &items[index]
		if !capabilityoverlay.IsImageOverlayItem(*item) {
			continue
		}
		if item.EndMs <= item.StartMs && item.DurationUS <= 0 {
			continue
		}
		if _, exists := item.Params["effects"]; exists {
			continue
		}
		if !capabilityoverlay.SelectImageEffectDecorationEnabled(planID, item.ID) {
			continue
		}
		name := capabilityoverlay.SelectImageEffectDecoration(planID, item.ID)
		if name == "" {
			continue
		}
		effect := capabilityoverlay.ImageEffectDecorationEffect(name, imageEffectDecorationSeed(planID, item.ID))
		if effect == nil {
			continue
		}
		if item.Params == nil {
			item.Params = map[string]any{}
		}
		item.Params["effects"] = []any{effect}
	}
}

// imageEffectDecorationSeed derives the decoration's per-item grain seed from
// the same plan + item identity as the selection itself, so the effect look is
// reproducible without carrying extra state in the plan.
func imageEffectDecorationSeed(planID, itemID string) uint32 {
	sum := digest.SHA256Sum([]byte("image-effect-seed\x00" + planID + "\x00" + itemID))
	return binary.BigEndian.Uint32(sum[:4])
}
