package cliprender

import (
	"testing"
)

// TestFingerprint_DoesNotMutateCaller pins ticket CR-19-08: Fingerprint()
// normalizes a deep copy, so an un-normalized request keeps its own zero values
// and presence flags. A shallow copy wrote through the shared nested pointers.
func TestFingerprint_DoesNotMutateCaller(t *testing.T) {
	req := &RenderRequest{
		SourceAssetID: "asset-source",
		Background:    &BackgroundSpec{},
		Watermark:     &WatermarkSpec{Enabled: true, Text: "watermark"},
		Transcript:    &TranscriptSpec{},
		Subtitles:     &SubtitlesSpec{Enabled: true},
		Output: &OutputSpec{
			Effects: []ClipEffectSpec{{Type: "glitch", Amount: 0.5}},
		},
		Audio:       &AudioSpec{},
		Destination: &DestinationSpec{},
		Execution:   &ExecutionSpec{},
		Overlays: []OverlayRefSpec{{
			RenderJobID:        "render-1",
			PlanFingerprint:    "plan-fp",
			RenderKey:          "key-1",
			SourceVideoAssetID: "asset-source",
			StartUS:            0,
			EndUS:              1000,
		}},
	}

	if _, err := req.Fingerprint(); err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"background.mode", req.Background.Mode, ""},
		{"background.kind", req.Background.Kind, ""},
		{"watermark.position", req.Watermark.Position, ""},
		{"watermark.opacity", req.Watermark.Opacity, float64(0)},
		{"watermark.margin_px", req.Watermark.MarginPX, 0},
		{"watermark.opacitySet", req.Watermark.opacitySet, false},
		{"watermark.marginSet", req.Watermark.marginSet, false},
		{"transcript.mode", req.Transcript.Mode, ""},
		{"transcript.language", req.Transcript.Language, ""},
		{"transcript.persist", req.Transcript.Persist, false},
		{"transcript.persistSet", req.Transcript.persistSet, false},
		{"subtitles.mode", req.Subtitles.Mode, ""},
		{"output.contract", req.Output.Contract, ""},
		{"output.width", req.Output.Width, 0},
		{"output.height", req.Output.Height, 0},
		{"output.fps_num", req.Output.FPSNum, 0},
		{"output.fps_den", req.Output.FPSDen, 0},
		{"audio.mode", req.Audio.Mode, ""},
		{"destination.drive_folder_id", req.Destination.DriveFolderID, ""},
	}
	for _, check := range checks {
		if check.got != check.want {
			t.Errorf("%s = %v, caller state was mutated (want %v)", check.name, check.got, check.want)
		}
	}
	if req.Watermark.Style != nil {
		t.Error("watermark.style was defaulted on the caller's block")
	}
	if req.Subtitles.Style != nil {
		t.Error("subtitles.style was defaulted on the caller's block")
	}
	if len(req.Output.Effects) != 1 || req.Output.Effects[0].Type != "glitch" {
		t.Errorf("output.effects slice was mutated: %+v", req.Output.Effects)
	}
	if len(req.Overlays) != 1 || req.Overlays[0].RenderJobID != "render-1" {
		t.Errorf("overlays slice was mutated: %+v", req.Overlays)
	}
}
