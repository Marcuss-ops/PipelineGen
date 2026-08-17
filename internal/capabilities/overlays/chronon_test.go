package overlays

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestCompileChrononPlanGolden pins the cross-repo golden invariant: the
// semantic GoldenOverlayPlanV1 compiles to exactly the chronon.render-plan.v1
// document RenderingGen executes as GoldenOverlayJobV1. This is the document
// PipelineGen submits through the queue; if it drifts from the RenderingGen
// golden, the whole chain diverges.
func TestCompileChrononPlanGolden(t *testing.T) {
	got, err := CompileChrononPlan(GoldenOverlayPlanV1())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Plan, GoldenChrononPlanV1) {
		gotJSON, _ := json.MarshalIndent(got.Plan, "", "  ")
		wantJSON, _ := json.MarshalIndent(GoldenChrononPlanV1, "", "  ")
		t.Fatalf("compiled golden plan diverges:\n--- got ---\n%s\n--- want ---\n%s", gotJSON, wantJSON)
	}
	if !reflect.DeepEqual(got.Assets, GoldenChrononAssetsV1) {
		t.Fatalf("compiled golden assets diverge:\n got %+v\nwant %+v", got.Assets, GoldenChrononAssetsV1)
	}
}

// TestCompileChrononPlanGoldenJSON matches the compiled plan against the
// literal render_plan document carried by RenderingGen's
// testdata/golden/golden-overlay-job-v1.json (the canonical, immutable
// golden). Both are canonicalized through JSON so field order and
// whitespace differences are ignored while every value must match.
func TestCompileChrononPlanGoldenJSON(t *testing.T) {
	const renderingGenGoldenRenderPlan = `{
    "schema": "chronon.render-plan",
    "version": 1,
    "job_id": "golden-overlay-v1",
    "canvas": { "width": 1280, "height": 720, "fps": 30, "duration_frames": 150 },
    "layers": [
      {
        "id": "background",
        "type": "image",
        "asset": "assets/background.jpg",
        "box_width": 1280,
        "box_height": 720,
        "fit": "cover",
        "start_frame": 0,
        "duration_frames": 150
      },
      {
        "id": "important_phrase",
        "type": "text",
        "text": "QUESTO CAMBIA TUTTO",
        "font": "assets/fonts/DejaVuSans.ttf",
        "preset": "title_centered",
        "start_frame": 20,
        "duration_frames": 41
      },
      {
        "id": "important_word",
        "type": "text",
        "text": "APPLE",
        "font": "assets/fonts/DejaVuSans.ttf",
        "preset": "kinetic_word",
        "start_frame": 65,
        "duration_frames": 31
      },
      {
        "id": "image_overlay",
        "type": "image",
        "asset": "assets/apple.png",
        "box_width": 260,
        "box_height": 260,
        "fit": "contain",
        "position": [380, 0],
        "start_frame": 90,
        "duration_frames": 46
      }
    ],
    "output": { "path": "result.mp4", "format": "mp4", "codec": "h264" }
  }`

	got, err := CompileChrononPlan(GoldenOverlayPlanV1())
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
	if err := json.Unmarshal([]byte(renderingGenGoldenRenderPlan), &wantDoc); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotDoc, wantDoc) {
		t.Fatalf("compiled plan does not match the RenderingGen golden document:\n got %s", gotJSON)
	}
}

// TestCompileChrononPlanAssets pins the content-addressed assets: the queue
// job must carry the same (hash, logical_path) pairs as the RenderingGen
// golden job's assets array, so materialization resolves every layer asset.
func TestCompileChrononPlanAssets(t *testing.T) {
	const renderingGenGoldenAssets = `[
      { "hash": "52209ee36928dba960583179922a54acf045d52d44c3128c517425d4baaa4f78", "logical_path": "assets/background.jpg" },
      { "hash": "ed873745e76173b66999c63546770d9f1426a2189515149176c67637e99a62d6", "logical_path": "assets/apple.png" },
      { "hash": "690243adfefe0ce154b547db6205794bd30ac4277275179517a90994f4980648", "logical_path": "assets/fonts/DejaVuSans.ttf" }
    ]`

	got, err := CompileChrononPlan(GoldenOverlayPlanV1())
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
	if err := json.Unmarshal([]byte(renderingGenGoldenAssets), &wantAssets); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotAssets, wantAssets) {
		t.Fatalf("compiled assets do not match the RenderingGen golden:\n got %s", gotJSON)
	}
}

func TestCompileChrononPlanRejectsUnsupportedTemplate(t *testing.T) {
	plan := GoldenOverlayPlanV1()
	plan.Items[0].TemplateID = "FLYING_LOGO" // no template spec
	if _, err := CompileChrononPlan(plan); err == nil {
		t.Fatal("expected unsupported template error")
	}
}

func TestCompileChrononPlanRejectsForeignRenderer(t *testing.T) {
	plan := GoldenOverlayPlanV1()
	plan.RendererVersion = "velox"
	if _, err := CompileChrononPlan(plan); err == nil {
		t.Fatal("expected foreign renderer error")
	}
}

// TestCompileChrononPlanParamsOverrides verifies per-item Params override the
// template defaults for fit/position/box, so callers can nudge geometry
// without new templates.
func TestCompileChrononPlanParamsOverrides(t *testing.T) {
	plan := GoldenOverlayPlanV1()
	plan.Items[3].Params = map[string]any{
		"fit":        "stretch",
		"position":   []any{100.0, 200.0},
		"box_width":  json.Number("320"),
		"box_height": int64(180),
	}
	got, err := CompileChrononPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	layer := got.Plan.Layers[3]
	if layer.Fit != "stretch" || layer.BoxWidth != 320 || layer.BoxHeight != 180 {
		t.Fatalf("params override not applied: %+v", layer)
	}
	if len(layer.Position) != 2 || layer.Position[0] != 100 || layer.Position[1] != 200 {
		t.Fatalf("params position override not applied: %+v", layer.Position)
	}
}

// TestCompileChrononPlanFrameMath pins the deterministic ms→frame projection
// used for the golden timing (5s @ 30fps = 150 frames).
func TestCompileChrononPlanFrameMath(t *testing.T) {
	got, err := CompileChrononPlan(GoldenOverlayPlanV1())
	if err != nil {
		t.Fatal(err)
	}
	if got.Plan.Canvas.DurationFrames != 150 {
		t.Fatalf("duration_frames = %d, want 150", got.Plan.Canvas.DurationFrames)
	}
	want := map[string][2]int64{
		"background":       {0, 150},
		"important_phrase": {20, 41},
		"important_word":   {65, 31},
		"image_overlay":    {90, 46},
	}
	for _, layer := range got.Plan.Layers {
		w, ok := want[layer.ID]
		if !ok {
			t.Fatalf("unexpected layer %q", layer.ID)
		}
		if layer.StartFrame != w[0] || layer.DurationFrames != w[1] {
			t.Errorf("layer %s: got start=%d dur=%d, want start=%d dur=%d", layer.ID, layer.StartFrame, layer.DurationFrames, w[0], w[1])
		}
	}
}
