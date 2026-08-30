package wiring

// chronon_plan_projector_test.go — the canonical projection of the sealed
// ClipRenderPlanV1 onto the Chronon render-plan v1 wire shape. The golden
// test locks byte-identical output for plans without style/background (the
// legacy map[string]any serialization), the style/transition tests lock the
// new projections, and the blur_source test locks fail-closed behaviour.

import (
	"encoding/json"
	"strings"
	"testing"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func projectorPlan() cliprender.ClipRenderPlanV1 {
	return cliprender.ClipRenderPlanV1{
		RunID:  "run-1",
		Source: cliprender.PlanSource{Path: "/tmp/source.mp4"},
		Output: cliprender.PlanOutput{Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1},
	}
}

// TestChrononPlanProjector_GoldenMinimal locks byte-identical output to the
// legacy map[string]any serialization for a plan with no watermark,
// subtitles or background. The projector is a pure refactor for this case —
// zero drift on the wire.
func TestChrononPlanProjector_GoldenMinimal(t *testing.T) {
	rp, err := (ChrononPlanProjector{}).Project(projectorPlan(), 8000)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	raw, err := json.Marshal(rp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"canvas":{"duration_frames":240,"fps_num":30,"fps_den":1,"height":720,"width":1280},"job_id":"run-1","layers":[{"duration_frames":240,"fit":"stretch","id":"video","source":"clip.mp4","start_frame":0,"type":"video"}],"output":{"codec":"h264","format":"mp4","path":"chronon.mp4"},"schema":"chronon.render-plan","version":1}`
	if string(raw) != want {
		t.Fatalf("golden plan mismatch\n got: %s\nwant: %s", raw, want)
	}
}

// TestChrononPlanProjector_FramesDerivation locks the single-owner canvas
// math: frames = ceil(durationMS × fps / 1000) and transition
// enter.duration_frames = ceil(ms × fps / 1000).
func TestChrononPlanProjector_FramesDerivation(t *testing.T) {
	plan := projectorPlan()
	plan.Output.FPSNum = 24
	plan.Output.FPSDen = 1
	rp, err := (ChrononPlanProjector{}).Project(plan, 8010)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if rp.Canvas.FPSNum != 24 || rp.Canvas.FPSDen != 1 || rp.Canvas.DurationFrames != 193 {
		t.Fatalf("canvas = %+v, want fps 24 / frames 193 (ceil(8.01×24))", rp.Canvas)
	}

	if anim := projectTransition(&scriptpkg.VideoVisualStyleSpec{
		TransitionIn: &scriptpkg.VideoTransitionSpec{Preset: "fade_in", DurationMS: 250},
	}, 30); anim == nil || anim.Preset != "fade_in" || anim.Enter == nil || anim.Enter.DurationFrames != 8 {
		t.Fatalf("transition = %+v, want fade_in enter 8 frames (250ms×30)", anim)
	}
	if anim := projectTransition(nil, 30); anim != nil {
		t.Fatalf("nil style → animation = %+v, want nil", anim)
	}
	if anim := projectTransition(&scriptpkg.VideoVisualStyleSpec{}, 30); anim != nil {
		t.Fatalf("empty style → animation = %+v, want nil", anim)
	}
}

// TestChrononPlanProjector_TextWatermarkStyleAndTransition verifies the text
// watermark projects its canonical style: style fill is baked into the layer
// color (RGB + watermark opacity alpha), style.font_size overrides the 64px
// default, the style block carries fill/shadow, and transition_in becomes the
// layer animation intent.
func TestChrononPlanProjector_TextWatermarkStyleAndTransition(t *testing.T) {
	plan := projectorPlan()
	plan.Watermark = &cliprender.PlanWatermark{
		Text:     "PG",
		Position: cliprender.PositionCenter,
		Opacity:  0.9,
		Style: &scriptpkg.VideoVisualStyleSpec{
			Color:      "#FFFFFF",
			FontSizePX: 58,
			Shadow: &scriptpkg.VideoShadowSpec{
				Color:   "#000000",
				Opacity: 0.6,
				BlurPX:  14,
				OffsetX: 0,
				OffsetY: 8,
			},
			TransitionIn: &scriptpkg.VideoTransitionSpec{Preset: "fade_in", DurationMS: 250},
		},
	}
	rp, err := (ChrononPlanProjector{}).Project(plan, 8000)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if len(rp.Layers) != 2 {
		t.Fatalf("layers = %d, want 2 (video + watermark)", len(rp.Layers))
	}
	wm := rp.Layers[1]
	if wm.Type != "text" || wm.FontSize != 58 {
		t.Fatalf("watermark = %+v, want text with font_size 58", wm)
	}
	if len(wm.Color) != 4 || wm.Color[0] != 1 || wm.Color[1] != 1 || wm.Color[2] != 1 || wm.Color[3] != 0.9 {
		t.Fatalf("color = %v, want [1 1 1 0.9] (fill RGB + watermark opacity)", wm.Color)
	}
	if wm.Style == nil || wm.Style.Fill != "#FFFFFF" || wm.Style.FontSize != 58 || wm.Style.Shadow == nil {
		t.Fatalf("style = %+v, want fill/font_size/shadow", wm.Style)
	}
	if wm.Style.Shadow.Color != "#000000" || wm.Style.Shadow.Opacity != 0.6 || wm.Style.Shadow.Blur != 14 {
		t.Fatalf("shadow = %+v", wm.Style.Shadow)
	}
	if wm.Style.Shadow.Offset[0] != 0 || wm.Style.Shadow.Offset[1] != 8 {
		t.Fatalf("shadow offset = %v, want [0 8]", wm.Style.Shadow.Offset)
	}
	if wm.Animation == nil || wm.Animation.Preset != "fade_in" || wm.Animation.Enter == nil || wm.Animation.Enter.DurationFrames != 8 {
		t.Fatalf("animation = %+v, want fade_in enter 8", wm.Animation)
	}
}

// TestChrononPlanProjector_ImageWatermarkStyleDims verifies the image
// watermark projects style.width_px/height_px onto the layer box AND that the
// position is resolved against the STYLED size (not the original image size,
// which may not even exist yet).
func TestChrononPlanProjector_ImageWatermarkStyleDims(t *testing.T) {
	plan := projectorPlan()
	plan.Watermark = &cliprender.PlanWatermark{
		Path:     "/tmp/logo_pulse.png", // intentionally absent on disk
		Position: cliprender.PositionTopRight,
		MarginPX: 24,
		Opacity:  0.9,
		Style: &scriptpkg.VideoVisualStyleSpec{
			WidthPX:      180,
			HeightPX:     90,
			TransitionIn: &scriptpkg.VideoTransitionSpec{Preset: "fade_in", DurationMS: 250},
		},
	}
	rp, err := (ChrononPlanProjector{}).Project(plan, 8000)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	wm := rp.Layers[1]
	if wm.Type != "image" || wm.BoxWidth != 180 || wm.BoxHeight != 90 {
		t.Fatalf("watermark = %+v, want image box 180x90 from style", wm)
	}
	// top_right, canvas 1280x720, margin 24, styled 180x90:
	// x = 1280−24−180 = 1076, y = 24 → world center x = 1076+90−640 = 526,
	// y = 24+45−360 = −291.
	if len(wm.Position) != 2 || wm.Position[0] != 526 || wm.Position[1] != -291 {
		t.Fatalf("position = %v, want [526 -291] (resolved against styled size)", wm.Position)
	}
	if wm.Opacity == nil || *wm.Opacity != 0.9 {
		t.Fatalf("opacity = %v, want 0.9", wm.Opacity)
	}
	if wm.Animation == nil || wm.Animation.Enter == nil || wm.Animation.Enter.DurationFrames != 8 {
		t.Fatalf("animation = %+v, want fade_in enter 8", wm.Animation)
	}
}

// TestChrononPlanProjector_SubtitlesStyleAndTransition verifies the subtitle
// track keeps its canonical style projection (fill/shadow/font_size) and now
// also lowers transition_in onto the layer animation intent.
func TestChrononPlanProjector_SubtitlesStyleAndTransition(t *testing.T) {
	plan := projectorPlan()
	plan.Subtitles = &cliprender.PlanSubtitles{
		Mode:   cliprender.SubtitlesModeBurn,
		Path:   "/tmp/subtitles.ass",
		SHA256: "abc",
		Cues:   []cliprender.Cue{{StartMs: 0, EndMs: 1000, Text: "hello"}},
		Style: &scriptpkg.VideoVisualStyleSpec{
			Color:      "#FFFFFF",
			FontSizePX: 54,
			Shadow: &scriptpkg.VideoShadowSpec{
				Color:   "#000000",
				Opacity: 0.7,
				BlurPX:  10,
				OffsetX: 0,
				OffsetY: 5,
			},
			TransitionIn: &scriptpkg.VideoTransitionSpec{Preset: "fade_in", DurationMS: 120},
		},
	}
	rp, err := (ChrononPlanProjector{}).Project(plan, 8000)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	sub := rp.Layers[1]
	if sub.Type != "subtitle_track" || sub.Format != "ass" || sub.BoxWidth != 1184 {
		t.Fatalf("subtitle layer = %+v, want subtitle_track/ass box 1184 (1280−96)", sub)
	}
	if sub.Style == nil || sub.Style.Fill != "#FFFFFF" || sub.Style.FontSize != 54 || sub.Style.Shadow == nil {
		t.Fatalf("style = %+v, want fill/font_size/shadow", sub.Style)
	}
	if sub.Style.Shadow.Opacity != 0.7 || sub.Style.Shadow.Blur != 10 || sub.Style.Shadow.Offset[1] != 5 {
		t.Fatalf("shadow = %+v", sub.Style.Shadow)
	}
	if sub.Animation == nil || sub.Animation.Preset != "fade_in" || sub.Animation.Enter == nil || sub.Animation.Enter.DurationFrames != 4 {
		t.Fatalf("animation = %+v, want fade_in enter 4 (120ms×30)", sub.Animation)
	}
}

// TestChrononPlanProjector_BackgroundAssetAndBlurFailClosed verifies the
// background projection: mode=asset rides on the video layer's background
// block, and mode=blur_source is REFUSED (Chronon has no video blur
// primitive — silently dropping the background would publish a wrong video).
func TestChrononPlanProjector_BackgroundAssetAndBlurFailClosed(t *testing.T) {
	asset := projectorPlan()
	asset.Background = &cliprender.PlanBackground{Mode: cliprender.BackgroundModeAsset, AssetID: "bg", Path: "/tmp/bg.png"}
	rp, err := (ChrononPlanProjector{}).Project(asset, 8000)
	if err != nil {
		t.Fatalf("Project(asset): %v", err)
	}
	video := rp.Layers[0]
	if video.Background == nil || video.Background.Asset != "background.png" || video.Background.Fit != "cover" {
		t.Fatalf("video background = %+v, want {background.png cover}", video.Background)
	}

	blur := projectorPlan()
	blur.Background = &cliprender.PlanBackground{Mode: cliprender.BackgroundModeBlurSource}
	_, err = (ChrononPlanProjector{}).Project(blur, 8000)
	if err == nil {
		t.Fatal("blur_source must fail closed (Chronon has no video blur primitive)")
	}
	if !strings.Contains(err.Error(), cliprender.BackgroundModeBlurSource) {
		t.Fatalf("error must name the unsupported mode, got: %v", err)
	}
}

// TestChrononPlanProjector_ParseHexColor locks the shared hex contract:
// "#RRGGBB" → normalized [r, g, b]; anything else is rejected.
func TestChrononPlanProjector_ParseHexColor(t *testing.T) {
	cases := []struct {
		in   string
		want [3]float64
		ok   bool
	}{
		{"#FFC107", [3]float64{1, 0.7569, 0.0275}, true},
		{"#000000", [3]float64{0, 0, 0}, true},
		{"#FFFFFF", [3]float64{1, 1, 1}, true},
		{"", [3]float64{}, false},
		{"#FFF", [3]float64{}, false},
		{"FFFFFF", [3]float64{}, false},
		{"#GGGGGG", [3]float64{}, false},
	}
	for _, tc := range cases {
		got, ok := parseHexColor(tc.in)
		if ok != tc.ok {
			t.Errorf("parseHexColor(%q) ok = %v, want %v", tc.in, ok, tc.ok)
			continue
		}
		if !ok {
			continue
		}
		for i := range got {
			if got[i] < tc.want[i]-0.0001 || got[i] > tc.want[i]+0.0001 {
				t.Errorf("parseHexColor(%q)[%d] = %v, want %v", tc.in, i, got[i], tc.want[i])
			}
		}
	}
}
