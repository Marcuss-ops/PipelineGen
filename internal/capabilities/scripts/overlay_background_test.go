package scriptgeneration

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestOverlayStyleParamsExposeTextRuntimeControls(t *testing.T) {
	fontSize, glow, stroke := 64.0, 18.0, 3.0
	got := overlayStyleParams(&scriptpkg.OverlayStyleSpec{
		FontFamily: "inter", Size: &scriptpkg.OverlaySizeSpec{FontSize: &fontSize},
		GlowSize: &glow, StrokeSize: &stroke,
	})
	for key, want := range map[string]any{"font_family": "inter", "font_size_px": 64.0, "glow_size": 18.0, "stroke_size": 3.0} {
		if got[key] != want {
			t.Errorf("%s = %#v, want %#v", key, got[key], want)
		}
	}
	style, ok := got["style"].(map[string]any)
	if !ok || style["font_size"] != 64.0 {
		t.Fatalf("nested style font size = %#v, want 64", got["style"])
	}
}

func TestOverlayStyleProjectsImageBackdropOnlyForStandaloneImages(t *testing.T) {
	zoom, blur := 1.2, 24.0
	items := []capmodel.OverlayItem{
		{ID: "solo", Kind: "image", AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "a"}}},
		{ID: "duo", Kind: "image", AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "a"}, {AssetID: "b"}}, ImageLayers: []capmodel.OverlayImageLayer{{ID: "left"}, {ID: "right"}}},
	}
	canvas := OverlayCanvasSpec{Style: &scriptpkg.OverlayStyleSpec{Image: &scriptpkg.OverlayImageStyleSpec{
		Backdrop: &scriptpkg.OverlayImageBackdropSpec{Zoom: &zoom, BlurPX: &blur,
			Grid: &scriptpkg.OverlayImageBackdropGridSpec{Color: "#7FA8C8"}},
	}}}
	applyOverlayStyleToItems(items, canvas)
	backdrop, ok := items[0].Params["image_backdrop"].(map[string]any)
	if !ok {
		t.Fatalf("standalone image params = %#v, want image_backdrop", items[0].Params)
	}
	if backdrop["zoom"] != 1.2 || backdrop["blur_px"] != 24.0 {
		t.Fatalf("backdrop = %#v, want zoom 1.2 / blur 24", backdrop)
	}
	if _, ok := items[1].Params["image_backdrop"]; ok {
		t.Fatalf("composite image must not receive image_backdrop: %#v", items[1].Params)
	}
}

func TestImageBackdropGridPayloadJSONProjectsToPlan(t *testing.T) {
	raw := []byte(`{
		"version": 2,
		"preset": "custom",
		"items": [{
			"source": {"type": "text", "topic": "image backdrop", "source_text": "Una sola fotografia."},
			"script_params": {"target_words": 100},
			"overlay_style": {"image": {
				"bounded": true, "fit": "contain",
				"backdrop": {"zoom": 1.2, "blur_px": 24,
					"grid": {"style": "lines", "color": "#7FA8C8", "spacing_px": 80, "line_width_px": 1.5, "opacity": 0.35}}
			}}
		}]
	}`)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var envelope scriptpkg.GenerationEnvelopeV2
	if err := decoder.Decode(&envelope); err != nil {
		t.Fatalf("decode script.generate payload: %v", err)
	}
	if err := envelope.Validate(); err != nil {
		t.Fatalf("validate script.generate payload: %v", err)
	}
	if len(envelope.Items) != 1 || envelope.Items[0].OverlayStyle == nil || envelope.Items[0].OverlayStyle.Image == nil {
		t.Fatalf("payload did not decode overlay_style.image: %+v", envelope.Items)
	}
	if err := envelope.Items[0].OverlayStyle.Validate(); err != nil {
		t.Fatalf("validate overlay_style: %v", err)
	}

	items := []capmodel.OverlayItem{{ID: "solo", Kind: "image", AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "a"}}}}
	applyOverlayStyleToItems(items, OverlayCanvasSpec{Style: envelope.Items[0].OverlayStyle})
	backdrop, ok := items[0].Params["image_backdrop"].(map[string]any)
	if !ok {
		t.Fatalf("payload did not project image_backdrop: %#v", items[0].Params)
	}
	if backdrop["zoom"] != 1.2 || backdrop["blur_px"] != 24.0 {
		t.Fatalf("backdrop = %#v, want zoom 1.2 / blur 24", backdrop)
	}
	grid, ok := backdrop["grid"].(map[string]any)
	if !ok {
		t.Fatalf("backdrop grid missing: %#v", backdrop)
	}
	if grid["style"] != "lines" || grid["color"] != "#7FA8C8" || grid["spacing_px"] != 80.0 || grid["line_width_px"] != 1.5 || grid["opacity"] != 0.35 {
		t.Fatalf("grid payload projection = %#v", grid)
	}
}

func TestRuntimeTextStyleTargetsPlannerRenderKinds(t *testing.T) {
	for _, kind := range []string{"text_phrase", "text_word", "number", "quote"} {
		if !isTextOverlayKind(kind) {
			t.Errorf("%q should receive runtime text style", kind)
		}
	}
	for _, kind := range []string{"image", "entity_image", "video_overlay"} {
		if isTextOverlayKind(kind) {
			t.Errorf("%q must not receive runtime text style", kind)
		}
	}
}

type testOverlayBackgroundSource struct {
	asset OverlayBackgroundAsset
	seen  string
}

func (s *testOverlayBackgroundSource) ResolveOverlayBackground(_ context.Context, id string) (OverlayBackgroundAsset, error) {
	s.seen = id
	return s.asset, nil
}

func TestResolveOverlayBackgroundEnrichesAssetIDOnlyPayload(t *testing.T) {
	source := &testOverlayBackgroundSource{asset: OverlayBackgroundAsset{
		AssetID: "classic1", LocalPath: "/cache/classic1.mp4", SHA256: "abc", MediaType: "video/mp4",
	}}
	runner := &Runner{overlayBackgroundSource: source}
	got, err := runner.resolveOverlayBackground(context.Background(), &scriptpkg.OverlayBackgroundSpec{Kind: "video", AssetID: "classic1", Fit: "cover", Loop: true})
	if err != nil {
		t.Fatal(err)
	}
	if source.seen != "classic1" {
		t.Fatalf("resolver saw %q, want classic1", source.seen)
	}
	if got.AssetID != "classic1" || got.LocalPath != "/cache/classic1.mp4" || got.SHA256 != "abc" || got.MediaType != "video/mp4" {
		t.Fatalf("resolved background = %+v", got)
	}
}

func TestResolveOverlayBackgroundColorNeedsNoAssetResolver(t *testing.T) {
	runner := &Runner{}
	got, err := runner.resolveOverlayBackground(context.Background(), &scriptpkg.OverlayBackgroundSpec{Kind: "color", Color: []float64{0, 0, 0, 1}})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Kind != "color" || len(got.Color) != 4 {
		t.Fatalf("color background was changed: %+v", got)
	}
}

func TestResolveOverlayBackgroundFailsClosedWithoutIdentity(t *testing.T) {
	_, err := (&Runner{}).resolveOverlayBackground(context.Background(), &scriptpkg.OverlayBackgroundSpec{Kind: "video"})
	if err == nil {
		t.Fatal("expected missing visual background identity to fail closed")
	}
}
