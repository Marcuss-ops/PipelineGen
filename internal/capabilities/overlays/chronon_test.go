package overlays

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
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

func TestCompileChrononPlanHonorsExplicitPresetID(t *testing.T) {
	plan := GoldenOverlayPlanV1()
	plan.PlanID = "explicit-special-name-preset"
	plan.Items = []OverlayItem{{
		ID: "person-name", TemplateID: "PERSON", Text: "Ada Lovelace",
		StartMs: 0, EndMs: 1000, PresetID: "name_glow_typewriter",
	}}
	compiled, err := CompileChrononPlan(plan)
	if err != nil {
		t.Fatalf("compile explicit preset: %v", err)
	}
	if len(compiled.Plan.Layers) != 1 || compiled.Plan.Layers[0].Preset != "name_glow_typewriter" {
		t.Fatalf("explicit preset was not transported to Chronon: %+v", compiled.Plan.Layers)
	}
}

func TestCompileChrononPlanOptionalBackground(t *testing.T) {
	plan := GoldenOverlayPlanV1()
	plan.PlanID = "background-payload"
	plan.Background = &OverlayBackground{Kind: "color", Color: []float64{0, 0, 0, 1}}
	compiled, err := CompileChrononPlan(plan)
	if err != nil {
		t.Fatalf("compile background payload: %v", err)
	}
	if len(compiled.Plan.Layers) == 0 || compiled.Plan.Layers[0].ID != "background" || compiled.Plan.Layers[0].Type != "color" {
		t.Fatalf("background was not emitted below overlays: %+v", compiled.Plan.Layers)
	}
	if got := compiled.Plan.Layers[0].DurationFrames; got != compiled.Plan.Canvas.DurationFrames {
		t.Fatalf("background duration=%d, canvas duration=%d", got, compiled.Plan.Canvas.DurationFrames)
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
    "canvas": { "width": 1280, "height": 720, "fps_num": 30, "fps_den": 1, "duration_frames": 150 },
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
        "font_asset": { "asset": "assets/fonts/DejaVuSans.ttf", "family": "DejaVu Sans", "weight": 700 },
        "text": "QUESTO CAMBIA TUTTO",
        "preset": "fast_fade_through",
        "start_frame": 20,
        "duration_frames": 41
      },
      {
        "id": "important_word",
        "type": "text",
        "font_asset": { "asset": "assets/fonts/DejaVuSans.ttf", "family": "DejaVu Sans", "weight": 700 },
        "text": "APPLE",
        "preset": "fast_fade_through",
        "start_frame": 65,
        "duration_frames": 31
      },
      {
        "id": "image_overlay",
        "type": "image",
        "asset": "assets/apple.png",
        "preset": "image_slide_right",
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

// TestCompileChrononPlan_MicrosecondTiming certifies that an OverlayPlan item
// carrying integer microseconds (start_us / duration_us) compiles to the exact
// frame range those microseconds imply — Chronon never rounds through the
// millisecond projection when microsecond timing is present.
func TestCompileChrononPlan_MicrosecondTiming(t *testing.T) {
	plan := OverlayPlan{
		SchemaVersion: SchemaVersionPlan,
		PlanID:        "us-plan",
		VideoID:       "us-video",
		Width:         1280,
		Height:        720,
		FPSNum:        30, FPSDen: 1,
		Items: []OverlayItem{{
			ID:         "precise",
			TemplateID: "IMPORTANT_PHRASE",
			Text:       "MICROSECOND",
			StartMs:    1240, // 1.24s
			EndMs:      5440, // 5.44s
			StartUS:    1_240_000,
			DurationUS: 4_200_000,
		}},
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("plan should validate: %v", err)
	}
	got, err := CompileChrononPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	layer := got.Plan.Layers[0]
	if layer.StartFrame != 37 {
		t.Fatalf("start_frame = %d, want 37 (1_240_000us @30fps)", layer.StartFrame)
	}
	if layer.DurationFrames != 126 {
		t.Fatalf("duration_frames = %d, want 126 (4_200_000us @30fps)", layer.DurationFrames)
	}
	// Canvas duration follows the microsecond end (5_440_000us → frame 163).
	if got.Plan.Canvas.DurationFrames != 163 {
		t.Fatalf("canvas duration_frames = %d, want 163", got.Plan.Canvas.DurationFrames)
	}
}

// TestCompileChrononPlan_HonorsMediaContract certifies the compiled output
// block derives container/codec/pixel format from the plan's media contract
// (alpha ProRes MOV), never a hardcoded mp4/h264 guess.
func TestCompileChrononPlan_HonorsMediaContract(t *testing.T) {
	plan := GoldenOverlayPlanV1()
	plan.MediaContract = DefaultOverlayContractV1.ID
	if err := plan.Validate(); err != nil {
		t.Fatalf("plan should validate: %v", err)
	}
	got, err := CompileChrononPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if got.Plan.Output.Path != "result.mov" {
		t.Fatalf("output path = %q, want result.mov", got.Plan.Output.Path)
	}
	if got.Plan.Output.Format != "mov" || got.Plan.Output.Codec != "prores" || got.Plan.Output.PixelFormat != "yuva444p" {
		t.Fatalf("output = %+v, want mov/prores/yuva444p", got.Plan.Output)
	}
}

// TestCompileChrononPlan_DefaultOutputUnchanged certifies the legacy (no
// contract) output stays mp4/h264 so the cross-repo golden is untouched.
func TestCompileChrononPlan_DefaultOutputUnchanged(t *testing.T) {
	got, err := CompileChrononPlan(GoldenOverlayPlanV1())
	if err != nil {
		t.Fatal(err)
	}
	if got.Plan.Output.Path != "result.mp4" || got.Plan.Output.Format != "mp4" || got.Plan.Output.Codec != "h264" {
		t.Fatalf("default output = %+v, want result.mp4/mp4/h264", got.Plan.Output)
	}
	if got.Plan.Output.PixelFormat != "" {
		t.Fatalf("default output must not carry a pixel format, got %q", got.Plan.Output.PixelFormat)
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
