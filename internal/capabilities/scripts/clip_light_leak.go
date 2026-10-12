package scriptgeneration

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

const clipStartLightLeakDurationUS int64 = 1_200_000

func clipStartLightLeakItems(planID string, timeline *audio.CanonicalTimeline) []capabilityoverlay.OverlayItem {
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
			durationUS := min(clipStartLightLeakDurationUS, clip.TimelineDurationUS)
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
