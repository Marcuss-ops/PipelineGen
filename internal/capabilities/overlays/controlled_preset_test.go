package overlays

import "testing"

// TestCompileControlledPresetsImagePhraseWord pins the deterministic,
// sampler-free certification scenario: one image + one phrase + one word with
// EXPLICIT presets (image_slide_left / clean_slide_up / snap_scale). Because
// every item carries PresetID, the compiled document must transport those
// exact presets regardless of the plan's deterministic sampler — this is the
// single controlled "matrix cell" the full 16-preset certification matrix is
// built from.
func TestCompileControlledPresetsImagePhraseWord(t *testing.T) {
	plan := OverlayPlan{
		SchemaVersion: SchemaVersionPlan,
		PlanID:        "controlled-preset-image-phrase-word",
		VideoID:       "video-controlled-preset",
		ProjectID:     "controlled-preset-cert",
		Width:         1280,
		Height:        720,
		FPSNum:        30, FPSDen: 1,
		RendererVersion: "chronon",
		Items: []OverlayItem{
			{
				ID:         "background_video",
				TemplateID: "VIDEO_BACKGROUND",
				StartMs:    0,
				EndMs:      6000,
				AssetRefs: []OverlayAssetRef{{
					AssetID: "background",
					URL:     "assets/background.mp4",
					SHA256:  GoldenBackgroundVideoHash,
				}},
			},
			{
				ID:         "image",
				TemplateID: "IMAGE_OVERLAY",
				PresetID:   "image_slide_left",
				StartMs:    500,
				EndMs:      3500,
				Params:     map[string]any{"box_width": 260, "box_height": 260},
				AssetRefs: []OverlayAssetRef{{
					AssetID: "fixture",
					URL:     "assets/overlay_globe.png",
					SHA256:  GoldenGlobeHash,
				}},
			},
			{
				ID:         "phrase",
				TemplateID: "IMPORTANT_PHRASE",
				PresetID:   "clean_slide_up",
				StartMs:    1000,
				EndMs:      4000,
				Text:       "MICHAEL JORDAN CHANGED BASKETBALL",
			},
			{
				ID:         "word",
				TemplateID: "IMPORTANT_WORD",
				PresetID:   "snap_scale",
				StartMs:    2000,
				EndMs:      4000,
				Text:       "LEGEND",
			},
		},
	}

	compiled, err := CompileChrononPlan(plan)
	if err != nil {
		t.Fatalf("compile controlled preset plan: %v", err)
	}

	type wantLayer struct {
		preset string
		text   string
		asset  string
		source string
		boxW   int
		boxH   int
		start  int64
		dur    int64
	}
	// Frame math at 30fps over a 6s (6000ms) clip.
	want := map[string]wantLayer{
		"background_video": {source: "assets/background.mp4", boxW: 1280, boxH: 720, start: 0, dur: 180},
		"image":            {preset: "image_slide_left", asset: "assets/overlay_globe.png", boxW: 260, boxH: 260, start: 15, dur: 90},
		"phrase":           {preset: "clean_slide_up", text: "MICHAEL JORDAN CHANGED BASKETBALL", start: 30, dur: 90},
		"word":             {preset: "snap_scale", text: "LEGEND", start: 60, dur: 60},
	}

	seen := make(map[string]bool, len(want))
	for _, layer := range compiled.Plan.Layers {
		w, ok := want[layer.ID]
		if !ok {
			t.Fatalf("unexpected layer %q", layer.ID)
		}
		seen[layer.ID] = true
		if layer.Preset != w.preset {
			t.Errorf("layer %s preset = %q, want %q", layer.ID, layer.Preset, w.preset)
		}
		if layer.Text != w.text {
			t.Errorf("layer %s text = %q, want %q", layer.ID, layer.Text, w.text)
		}
		if layer.Asset != w.asset {
			t.Errorf("layer %s asset = %q, want %q", layer.ID, layer.Asset, w.asset)
		}
		if layer.Source != w.source {
			t.Errorf("layer %s source = %q, want %q", layer.ID, layer.Source, w.source)
		}
		if layer.BoxWidth != w.boxW || layer.BoxHeight != w.boxH {
			t.Errorf("layer %s box = %dx%d, want %dx%d", layer.ID, layer.BoxWidth, layer.BoxHeight, w.boxW, w.boxH)
		}
		if layer.StartFrame != w.start || layer.DurationFrames != w.dur {
			t.Errorf("layer %s frame range = [%d +%d], want [%d +%d]", layer.ID, layer.StartFrame, layer.DurationFrames, w.start, w.dur)
		}
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("missing layer %q", id)
		}
	}

	if compiled.Plan.Canvas.DurationFrames != 180 {
		t.Errorf("canvas duration_frames = %d, want 180 (6s @ 30fps)", compiled.Plan.Canvas.DurationFrames)
	}
}
