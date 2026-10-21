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
		if _, explicit := item.Params["effects"]; explicit {
			continue
		}
		if len(item.ImageLayers) > 0 {
			assetsByID := make(map[string]capabilityoverlay.OverlayAssetRef, len(item.AssetRefs))
			for _, asset := range item.AssetRefs {
				assetsByID[asset.AssetID] = asset
			}
			for layerIndex := range item.ImageLayers {
				layer := &item.ImageLayers[layerIndex]
				_, exists := assetsByID[layer.AssetID]
				if !exists {
					continue
				}
				layer.Params = attachRandomImageEffect(planID, layer.ID, layer.Params)
			}
			continue
		}
		item.Params = attachRandomImageEffect(planID, item.ID, item.Params)
	}
}

func attachRandomImageEffect(planID, itemID string, params map[string]any) map[string]any {
	if _, explicit := params["effects"]; explicit || !capabilityoverlay.SelectImageEffectDecorationEnabled(planID, itemID) {
		return params
	}
	name := capabilityoverlay.SelectImageEffectDecoration(planID, itemID)
	if name == "" {
		return params
	}
	effect := capabilityoverlay.ImageEffectDecorationEffect(name, imageEffectDecorationSeed(planID, itemID))
	if effect == nil {
		return params
	}
	if params == nil {
		params = make(map[string]any)
	}
	params["effects"] = []any{effect}
	return params
}

// imageEffectDecorationSeed derives the decoration's per-item grain seed from
// the same plan + item identity as the selection itself, so the effect look is
// reproducible without carrying extra state in the plan.
func imageEffectDecorationSeed(planID, itemID string) uint32 {
	sum := digest.SHA256Sum([]byte("image-effect-seed\x00" + planID + "\x00" + itemID))
	return binary.BigEndian.Uint32(sum[:4])
}
