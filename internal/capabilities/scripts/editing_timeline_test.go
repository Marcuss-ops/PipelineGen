package scriptgeneration

import (
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// Type aliases to reference entity types without polluting the test namespace.
type testEntityTimeline = capabilityentities.EntityTimeline
type testSceneEntityTimeline = capabilityentities.SceneEntityTimeline
type testEntityOccurrence = capabilityentities.EntityOccurrence

func TestEditingTimeline_UsesCanonicalMicroseconds(t *testing.T) {
	result := &GenerateResult{
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{
			Version:    capabilityaudio.TimelineVersion,
			DurationUS: 30000000,
			Segments: []capabilityaudio.TimelineSegment{
				{ID: "scene-01", Index: 0, TimelineStartUS: 0, DurationUS: 15000000},
				{ID: "scene-02", Index: 1, TimelineStartUS: 15000000, DurationUS: 15000000},
			},
		},
		FinalAudio: &FinalAudioReference{
			AssetID:          "final-audio-001",
			FinalAudioSHA256: "abc123",
			DurationUS:       30000000,
			DurationMS:       30000,
		},
	}
	et, err := BuildEditingTimeline(result)
	if err != nil {
		t.Fatalf("BuildEditingTimeline failed: %v", err)
	}
	if et == nil {
		t.Fatal("expected non-nil editing timeline")
	}
	if et.Version != "v1" {
		t.Errorf("version = %q, want v1", et.Version)
	}
	if et.Timebase != "us" {
		t.Errorf("timebase = %q, want us", et.Timebase)
	}
	if et.DurationUS != 30000000 {
		t.Errorf("duration_us = %d, want 30000000", et.DurationUS)
	}
	if et.Audio.DurationUS != 30000000 {
		t.Errorf("audio.duration_us = %d, want 30000000", et.Audio.DurationUS)
	}
}

func TestEditingTimeline_OverlayTimesMatchCanonicalTimeline(t *testing.T) {
	result := &GenerateResult{
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{
			Version:    capabilityaudio.TimelineVersion,
			DurationUS: 30000000,
			Segments: []capabilityaudio.TimelineSegment{
				{ID: "scene-01", Index: 0, TimelineStartUS: 0, DurationUS: 15000000},
			},
		},
		FinalAudio: &FinalAudioReference{
			AssetID:          "final-audio-001",
			FinalAudioSHA256: "abc123",
			DurationUS:       30000000,
			DurationMS:       30000,
		},
		EntityTimeline: func() *testEntityTimeline {
			tl := capabilityEntityTimeline(t)
			return &tl
		}(),
		OverlayPlan: &capabilityoverlay.OverlayPlan{
			SchemaVersion: capabilityoverlay.SchemaVersionPlan,
			PlanID:        "test-plan",
			VideoID:       "test-video",
			Width:         1920,
			Height:        1080,
			FPS:           30,
			Items: []capabilityoverlay.OverlayItem{
				{
					ID:         "overlay-scene-01-tom-hanks",
					SceneID:    "scene-01",
					EntityID:   "tom-hanks",
					Kind:       "entity_card",
					TemplateID: "person_default",
					Text:       "Tom Hanks",
					StartMs:    3240, // 3240ms = 3240000us
					EndMs:      5100, // 5100ms = 5100000us
				},
			},
		},
	}
	et, err := BuildEditingTimeline(result)
	if err != nil {
		t.Fatalf("BuildEditingTimeline failed: %v", err)
	}
	if len(et.Overlays) != 1 {
		t.Fatalf("expected 1 overlay, got %d", len(et.Overlays))
	}
	ov := et.Overlays[0]
	// EntityTimeline timing should win (microsecond precision):
	// AudioStartUS = 3240000, AudioEndUS = 5100000
	if ov.StartUS != 3240000 {
		t.Errorf("overlay start_us = %d, want 3240000 (from EntityTimeline)", ov.StartUS)
	}
	if ov.EndUS != 5100000 {
		t.Errorf("overlay end_us = %d, want 5100000 (from EntityTimeline)", ov.EndUS)
	}
	if ov.Entity != "Tom Hanks" {
		t.Errorf("overlay entity = %q, want Tom Hanks", ov.Entity)
	}
}

func TestEditingTimeline_SceneSpansAreContiguous(t *testing.T) {
	result := &GenerateResult{
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{
			Version:    capabilityaudio.TimelineVersion,
			DurationUS: 30000000,
			Segments: []capabilityaudio.TimelineSegment{
				{ID: "scene-01", Index: 0, TimelineStartUS: 0, DurationUS: 10000000},
				{ID: "scene-02", Index: 1, TimelineStartUS: 10000000, DurationUS: 10000000},
				{ID: "scene-03", Index: 2, TimelineStartUS: 20000000, DurationUS: 10000000},
			},
		},
		FinalAudio: &FinalAudioReference{
			AssetID:          "final-audio-001",
			FinalAudioSHA256: "abc123",
			DurationUS:       30000000,
			DurationMS:       30000,
		},
	}
	et, err := BuildEditingTimeline(result)
	if err != nil {
		t.Fatalf("BuildEditingTimeline failed: %v", err)
	}
	if len(et.Scenes) != 3 {
		t.Fatalf("expected 3 scenes, got %d", len(et.Scenes))
	}
	// Verify contiguity
	for i := 1; i < len(et.Scenes); i++ {
		if et.Scenes[i].StartUS != et.Scenes[i-1].EndUS {
			t.Errorf("scene[%d].start_us=%d != scene[%d].end_us=%d",
				i, et.Scenes[i].StartUS, i-1, et.Scenes[i-1].EndUS)
		}
	}
}

func TestEditingTimeline_AudioSHA256MatchesFinalAudio(t *testing.T) {
	result := &GenerateResult{
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{
			Version:    capabilityaudio.TimelineVersion,
			DurationUS: 10000000,
			Segments: []capabilityaudio.TimelineSegment{
				{ID: "scene-01", Index: 0, TimelineStartUS: 0, DurationUS: 10000000},
			},
		},
		FinalAudio: &FinalAudioReference{
			AssetID:          "final-audio-001",
			FinalAudioSHA256: "deadbeef12345678",
			DurationUS:       10000000,
			DurationMS:       10000,
			DriveLink:        "https://drive.google.com/file/d/abc",
		},
	}
	et, err := BuildEditingTimeline(result)
	if err != nil {
		t.Fatalf("BuildEditingTimeline failed: %v", err)
	}
	if et.Audio.SHA256 != "deadbeef12345678" {
		t.Errorf("audio sha256 = %q, want deadbeef12345678", et.Audio.SHA256)
	}
	if et.Audio.DriveLink != "https://drive.google.com/file/d/abc" {
		t.Errorf("audio drive_link = %q, want drive link", et.Audio.DriveLink)
	}
}

func TestEditingTimeline_OverlaySpansCarryRenderedArtifactIdentityAndContract(t *testing.T) {
	result := &GenerateResult{
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{
			Version:    capabilityaudio.TimelineVersion,
			DurationUS: 15000000,
			Segments: []capabilityaudio.TimelineSegment{
				{ID: "scene-01", Index: 0, TimelineStartUS: 0, DurationUS: 15000000},
			},
		},
		FinalAudio: &FinalAudioReference{
			AssetID:          "final-audio-001",
			FinalAudioSHA256: "abc123",
			DurationUS:       15000000,
			DurationMS:       15000,
		},
		OverlayPlan: &capabilityoverlay.OverlayPlan{
			SchemaVersion: capabilityoverlay.SchemaVersionPlan,
			PlanID:        "test-plan",
			VideoID:       "test-video",
			Width:         1920,
			Height:        1080,
			FPS:           30,
			MediaContract: "overlay-v1",
			Items: []capabilityoverlay.OverlayItem{
				{
					ID:         "overlay-scene-01-tom-hanks",
					SceneID:    "scene-01",
					Kind:       "entity_card",
					TemplateID: "person_default",
					Text:       "Tom Hanks",
					StartMs:    3240,
					EndMs:      5100,
				},
			},
		},
		OverlayRender: &RenderReference{
			JobID:  "test-plan",
			Status: "COMPLETED",
			Artifact: &RenderArtifact{
				SHA256:      "overlay-sha256",
				DriveLink:   "https://drive.google.com/file/d/overlay",
				DriveFileID: "drive-overlay-1",
			},
		},
	}
	et, err := BuildEditingTimeline(result)
	if err != nil {
		t.Fatalf("BuildEditingTimeline failed: %v", err)
	}
	if len(et.Overlays) != 1 {
		t.Fatalf("expected 1 overlay, got %d", len(et.Overlays))
	}
	ov := et.Overlays[0]
	if ov.SHA256 != "overlay-sha256" {
		t.Errorf("overlay sha256 = %q, want overlay-sha256", ov.SHA256)
	}
	if ov.DriveLink != "https://drive.google.com/file/d/overlay" {
		t.Errorf("overlay drive_link = %q, want drive link", ov.DriveLink)
	}
	if ov.MediaContract != "overlay-v1" {
		t.Errorf("overlay media_contract = %q, want overlay-v1", ov.MediaContract)
	}
}

func TestEditingTimeline_OverlaySpansWithoutRenderLeaveIdentityEmpty(t *testing.T) {
	result := &GenerateResult{
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{
			Version:    capabilityaudio.TimelineVersion,
			DurationUS: 15000000,
			Segments: []capabilityaudio.TimelineSegment{
				{ID: "scene-01", Index: 0, TimelineStartUS: 0, DurationUS: 15000000},
			},
		},
		FinalAudio: &FinalAudioReference{
			AssetID:          "final-audio-001",
			FinalAudioSHA256: "abc123",
			DurationUS:       15000000,
			DurationMS:       15000,
		},
		OverlayPlan: &capabilityoverlay.OverlayPlan{
			SchemaVersion: capabilityoverlay.SchemaVersionPlan,
			PlanID:        "test-plan",
			VideoID:       "test-video",
			Width:         1920,
			Height:        1080,
			FPS:           30,
			Items: []capabilityoverlay.OverlayItem{
				{
					ID:         "overlay-scene-01-tom-hanks",
					SceneID:    "scene-01",
					Kind:       "entity_card",
					TemplateID: "person_default",
					Text:       "Tom Hanks",
					StartMs:    3240,
					EndMs:      5100,
				},
			},
		},
	}
	et, err := BuildEditingTimeline(result)
	if err != nil {
		t.Fatalf("BuildEditingTimeline failed: %v", err)
	}
	if len(et.Overlays) != 1 {
		t.Fatalf("expected 1 overlay, got %d", len(et.Overlays))
	}
	ov := et.Overlays[0]
	if ov.SHA256 != "" {
		t.Errorf("overlay sha256 = %q, want empty when render absent", ov.SHA256)
	}
	if ov.DriveLink != "" {
		t.Errorf("overlay drive_link = %q, want empty when render absent", ov.DriveLink)
	}
}

func TestEditingTimeline_NilResultReturnsNil(t *testing.T) {
	et, err := BuildEditingTimeline(nil)
	if err != nil {
		t.Fatalf("nil result should not error: %v", err)
	}
	if et != nil {
		t.Error("nil result should return nil")
	}
}

func TestEditingTimeline_NilTimelineReturnsNil(t *testing.T) {
	result := &GenerateResult{
		FinalAudio: &FinalAudioReference{
			AssetID:    "final-audio-001",
			DurationUS: 10000000,
		},
	}
	et, err := BuildEditingTimeline(result)
	if err != nil {
		t.Fatalf("nil timeline should not error: %v", err)
	}
	if et != nil {
		t.Error("nil canonical timeline should return nil")
	}
}

func TestEditingTimeline_NilAudioReturnsNil(t *testing.T) {
	result := &GenerateResult{
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{
			Version:    capabilityaudio.TimelineVersion,
			DurationUS: 10000000,
			Segments: []capabilityaudio.TimelineSegment{
				{ID: "scene-01", Index: 0, TimelineStartUS: 0, DurationUS: 10000000},
			},
		},
	}
	et, err := BuildEditingTimeline(result)
	if err != nil {
		t.Fatalf("nil audio should not error: %v", err)
	}
	if et != nil {
		t.Error("nil final audio should return nil")
	}
}

func TestEditingTimeline_ValidateRejectsZeroDuration(t *testing.T) {
	et := EditingTimelineV1{
		Version:    EditingTimelineVersion,
		Timebase:   EditingTimebase,
		DurationUS: 0,
		Audio:      EditingAudioRef{AssetID: "a", SHA256: "b", DurationUS: 0},
	}
	if err := et.Validate(); err == nil {
		t.Error("zero duration should fail validation")
	}
}

func TestEditingTimeline_ValidateRejectsMismatchedDuration(t *testing.T) {
	et := EditingTimelineV1{
		Version:    EditingTimelineVersion,
		Timebase:   EditingTimebase,
		DurationUS: 10000000,
		Audio:      EditingAudioRef{AssetID: "a", SHA256: "b", DurationUS: 5000000},
	}
	if err := et.Validate(); err == nil {
		t.Error("mismatched audio/timeline duration should fail validation")
	}
}

// capabilityEntityTimeline builds a minimal EntityTimeline for testing.
func capabilityEntityTimeline(t *testing.T) testEntityTimeline {
	t.Helper()
	return testEntityTimeline{
		Version:    1,
		DurationUS: 30000000,
		Scenes: []testSceneEntityTimeline{
			{
				SceneID:          "scene-01",
				SceneIndex:       0,
				TimelineStartUS:  0,
				VoiceoverAssetID: "vo-001",
				Entities: []testEntityOccurrence{
					{
						EntityID:        "tom-hanks",
						Name:            "Tom Hanks",
						Type:            "PERSON",
						SceneID:         "scene-01",
						SceneIndex:      0,
						TextStart:       0,
						TextEnd:         9,
						WordStart:       0,
						WordEnd:         1,
						LocalStartUS:    3240000,
						LocalEndUS:      5100000,
						TimelineStartUS: 0,
						AudioStartUS:    3240000,
						AudioEndUS:      5100000,
						Confidence:      0.99,
					},
				},
			},
		},
	}
}
