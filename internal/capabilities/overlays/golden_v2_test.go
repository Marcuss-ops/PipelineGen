// Package overlays — golden_v2_test.go pins the cross-repo invariant for the
// universal benchmark workload (GoldenOverlayJobV2): PipelineGen's semantic
// GoldenOverlayPlanV2 must compile to exactly the chronon.render-plan.v1
// document RenderingGen executes as GoldenOverlayJobV2
// (RenderingGen/testdata/golden/golden-overlay-job-v2.json +
// renderinggen/internal/chronon/golden_v2.go), animations included.
package overlays

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestCompileChrononPlanGoldenV2 pins the cross-repo golden invariant for the
// v2 benchmark workload: the semantic plan compiles to exactly the compiled
// golden struct (layers, canvas, output and content-addressed assets).
func TestCompileChrononPlanGoldenV2(t *testing.T) {
	got, err := CompileChrononPlan(GoldenOverlayPlanV2())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Plan, GoldenChrononPlanV2) {
		gotJSON, _ := json.MarshalIndent(got.Plan, "", "  ")
		wantJSON, _ := json.MarshalIndent(GoldenChrononPlanV2, "", "  ")
		t.Fatalf("compiled v2 golden plan diverges:\n--- got ---\n%s\n--- want ---\n%s", gotJSON, wantJSON)
	}
	if !reflect.DeepEqual(got.Assets, GoldenChrononAssetsV2) {
		t.Fatalf("compiled v2 golden assets diverge:\n got %+v\nwant %+v", got.Assets, GoldenChrononAssetsV2)
	}
}

// TestCompileChrononPlanGoldenV2JSON matches the compiled v2 plan against the
// literal render_plan document carried by RenderingGen's
// testdata/golden/golden-overlay-job-v2.json (the canonical, immutable
// benchmark). Both are canonicalized through JSON so field order and
// whitespace differences are ignored while every value must match —
// including the layer animation presets.
func TestCompileChrononPlanGoldenV2JSON(t *testing.T) {
	const renderingGenGoldenV2RenderPlan = `{
    "schema": "chronon.render-plan",
    "version": 1,
    "job_id": "golden-overlay-v2",
    "canvas": { "width": 1280, "height": 720, "fps": 30, "duration_frames": 240 },
    "layers": [
      {
        "id": "background_video",
        "type": "video",
        "source": "assets/background.mp4",
        "box_width": 1280,
        "box_height": 720,
        "fit": "cover",
        "start_frame": 0,
        "duration_frames": 240
      },
      {
        "id": "important_phrase_1",
        "type": "text",
        "text": "IL FUTURO È ADESSO",
        "preset": "caption_card",
        "start_frame": 24,
        "duration_frames": 84,
        "animation": { "preset": "fade_in" }
      },
      {
        "id": "important_word_1",
        "type": "text",
        "text": "VELOCITÀ",
        "preset": "active_word_pop",
        "start_frame": 24,
        "duration_frames": 84,
        "animation": { "preset": "scale_drop" }
      },
      {
        "id": "image_overlay_1",
        "type": "image",
        "asset": "assets/overlay_globe.png",
        "preset": "image_focus_in",
        "box_width": 300,
        "box_height": 300,
        "fit": "contain",
        "position": [380, 0],
        "start_frame": 24,
        "duration_frames": 132
      },
      {
        "id": "important_phrase_2",
        "type": "text",
        "text": "CAMBIARE IL MERCATO",
        "preset": "caption_card",
        "start_frame": 132,
        "duration_frames": 84,
        "animation": { "preset": "fade_shift_vertical" }
      },
      {
        "id": "important_word_2",
        "type": "text",
        "text": "POTENZA",
        "preset": "active_word_pop",
        "start_frame": 132,
        "duration_frames": 84,
        "animation": { "preset": "fade_in" }
      },
      {
        "id": "image_overlay_2",
        "type": "image",
        "asset": "assets/overlay_chart.png",
        "preset": "image_focus_in",
        "box_width": 300,
        "box_height": 300,
        "fit": "contain",
        "position": [840, 380],
        "start_frame": 120,
        "duration_frames": 108
      },
      {
        "id": "logo",
        "type": "image",
        "asset": "assets/logo_pulse.png",
        "box_width": 160,
        "box_height": 160,
        "fit": "contain",
        "position": [1060, 40],
        "start_frame": 0,
        "duration_frames": 240
      }
    ],
    "output": { "path": "result.mp4", "format": "mp4", "codec": "h264" }
  }`

	got, err := CompileChrononPlan(GoldenOverlayPlanV2())
	if err != nil {
		t.Fatal(err)
	}
	gotJSON, err := got.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	var gotDoc, wantDoc any
	if err := json.Unmarshal(gotJSON, &gotDoc); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(renderingGenGoldenV2RenderPlan), &wantDoc); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotDoc, wantDoc) {
		t.Fatalf("compiled v2 plan does not match the RenderingGen golden document:\n got %s", gotJSON)
	}
}

// TestCompileChrononPlanGoldenV2Assets pins the content-addressed assets: the
// queue job must carry the same (hash, logical_path) pairs as the RenderingGen
// v2 golden job's assets array (background video, globe, chart, logo, font).
func TestCompileChrononPlanGoldenV2Assets(t *testing.T) {
	const renderingGenGoldenV2Assets = `[
      { "hash": "11eeec4e47fb254dc6acc4b72405bf06f2ed7294cffa8b7a95ba76f1e8d9c70c", "logical_path": "assets/background.mp4" },
      { "hash": "b7219a0c2f3f0c45f12d3b387332bd2cf0502205a6bb3fa0beca542de6da1939", "logical_path": "assets/overlay_globe.png" },
      { "hash": "efb77ea94d50f178e970841f641be7f4efc59837d5628fec2f4bdb6e88a9f73a", "logical_path": "assets/overlay_chart.png" },
      { "hash": "15d17403acaf45cdeeb9dad6a6c88e3b5f686b3ee1961cc97815b4830d3b981f", "logical_path": "assets/logo_pulse.png" },
      { "hash": "690243adfefe0ce154b547db6205794bd30ac4277275179517a90994f4980648", "logical_path": "assets/fonts/DejaVuSans.ttf" }
    ]`

	got, err := CompileChrononPlan(GoldenOverlayPlanV2())
	if err != nil {
		t.Fatal(err)
	}
	gotJSON, err := json.Marshal(got.Assets)
	if err != nil {
		t.Fatal(err)
	}
	var gotAssets, wantAssets any
	if err := json.Unmarshal(gotJSON, &gotAssets); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(renderingGenGoldenV2Assets), &wantAssets); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotAssets, wantAssets) {
		t.Fatalf("compiled v2 assets do not match the RenderingGen golden:\n got %s", gotJSON)
	}
}
