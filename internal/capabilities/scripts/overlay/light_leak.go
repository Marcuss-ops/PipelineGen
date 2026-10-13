// Package overlay — light_leak.go owns the random light-leak overlay
// derivation. Moved from the root scripts package (2026-10-13): the root
// package is the compatibility facade and each overlay family migrates into
// this subpackage. The registered scripts hotspot must never gain
// production files, so overlay policy lives here; behaviour is unchanged.
package overlay

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

const ClipStartLightLeakDurationUS int64 = 1_200_000

func ClipStartLightLeakItems(planID string, timeline *audio.CanonicalTimeline) []capabilityoverlay.OverlayItem {
	if timeline == nil || timeline.DurationUS <= 0 {
		return nil
	}
	var items []capabilityoverlay.OverlayItem
	for _, scene := range timeline.Segments {
		for index, clip := range scene.EffectiveVideoSegments() {
			if clip.AssetID == "" || clip.Freeze || clip.TimelineDurationUS <= 0 {
				continue
			}
			startUS := scene.TimelineStartUS + clip.TimelineOffsetUS
			if startUS < 0 || startUS >= timeline.DurationUS {
				continue
			}
			durationUS := min(ClipStartLightLeakDurationUS, clip.TimelineDurationUS)
			if remaining := timeline.DurationUS - startUS; durationUS > remaining {
				durationUS = remaining
			}
			if durationUS <= 0 {
				continue
			}
			id := fmt.Sprintf("light-leak-clip-%s", digest.SHA256String(fmt.Sprintf("%s\x00%d\x00%s", scene.ID, index, clip.AssetID))[:16])
			items = append(items, capabilityoverlay.OverlayItem{
				ID: id, SceneID: scene.ID, GroupID: scene.ID, SubgroupID: id,
				Kind: "light_leak", TemplateID: "LIGHT_LEAK",
				MotionID: capabilityoverlay.SelectLightLeakMotion(planID, id),
				StartUS:  startUS, DurationUS: durationUS,
				StartMs: startUS / 1000, EndMs: (startUS + durationUS + 999) / 1000,
			})
		}
	}
	return items
}

func OverlayStartLightLeakItems(planID string, overlays []capabilityoverlay.OverlayItem) []capabilityoverlay.OverlayItem {
	var items []capabilityoverlay.OverlayItem
	for _, overlay := range overlays {
		if overlay.Kind == "light_leak" || overlay.DurationUS <= 0 || overlay.ID == "" {
			continue
		}
		id := "light-leak-overlay-" + digest.SHA256String(planID + "\x00" + overlay.ID)[:16]
		duration := min(ClipStartLightLeakDurationUS, overlay.DurationUS)
		items = append(items, capabilityoverlay.OverlayItem{
			ID: id, SceneID: overlay.SceneID, GroupID: overlay.GroupID, SubgroupID: id,
			Kind: "light_leak", TemplateID: "LIGHT_LEAK",
			MotionID: capabilityoverlay.SelectLightLeakMotion(planID, id),
			StartUS:  overlay.StartUS, DurationUS: duration,
			StartMs: overlay.StartUS / 1000, EndMs: (overlay.StartUS + duration + 999) / 1000,
		})
	}
	return items
}
