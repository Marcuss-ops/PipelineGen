package scriptgeneration

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

func TestClipStartLightLeakItemsTargetRealVideoSegments(t *testing.T) {
	timeline := &audio.CanonicalTimeline{DurationUS: 8_000_000, Segments: []audio.TimelineSegment{{
		ID: "scene-1", TimelineStartUS: 1_000_000, DurationUS: 6_000_000,
		VideoSegments: []audio.VideoSegment{
			{AssetID: "clip-a", TimelineOffsetUS: 0, TimelineDurationUS: 2_500_000},
			{AssetID: "freeze", TimelineOffsetUS: 2_500_000, TimelineDurationUS: 1_000_000, Freeze: true},
			{AssetID: "clip-b", TimelineOffsetUS: 3_500_000, TimelineDurationUS: 1_500_000},
		},
	}}}
	got := clipStartLightLeakItems("plan", timeline)
	if len(got) != 2 {
		t.Fatalf("light leak items = %d, want one per real clip", len(got))
	}
	if got[0].TemplateID != "LIGHT_LEAK" || got[0].Kind != "light_leak" || got[0].StartUS != 1_000_000 || got[0].DurationUS != clipStartLightLeakDurationUS {
		t.Fatalf("first light leak = %+v", got[0])
	}
	if got[1].StartUS != 4_500_000 {
		t.Fatalf("second light leak start = %d, want 4500000", got[1].StartUS)
	}
	if !containsString(capabilityoverlay.CertifiedLightLeakMotions(), got[0].MotionID) {
		t.Fatalf("uncertified light leak motion %q", got[0].MotionID)
	}
}

func TestOverlayStartLightLeakItemsTargetFinalizedOverlays(t *testing.T) {
	got := overlayStartLightLeakItems("plan", []capabilityoverlay.OverlayItem{
		{ID: "phrase-1", SceneID: "scene-1", GroupID: "group-1", Kind: "phrase", StartUS: 2_000_000, DurationUS: 3_000_000},
		{ID: "leak-1", Kind: "light_leak", StartUS: 3_000_000, DurationUS: 1_000_000},
		{ID: "instant", Kind: "image", StartUS: 4_000_000},
	})
	if len(got) != 1 {
		t.Fatalf("overlay-start leaks = %d, want one per eligible visual overlay", len(got))
	}
	if got[0].Kind != "light_leak" || got[0].TemplateID != "LIGHT_LEAK" || got[0].StartUS != 2_000_000 || got[0].DurationUS != clipStartLightLeakDurationUS {
		t.Fatalf("overlay-start leak = %+v", got[0])
	}
	if !containsString(capabilityoverlay.CertifiedLightLeakMotions(), got[0].MotionID) {
		t.Fatalf("uncertified light leak motion %q", got[0].MotionID)
	}
}
