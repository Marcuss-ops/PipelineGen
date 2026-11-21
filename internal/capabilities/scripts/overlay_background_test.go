package scriptgeneration

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestOverlayStyleParamsExposeTextRuntimeControls(t *testing.T) {
	fontSize, glow, stroke := 64.0, 18.0, 3.0
	got := capabilityoverlay.StyleParams(&scriptpkg.OverlayStyleSpec{
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

func TestOverlayStyleProjectsImageBackdropWithMemberOwnedPhotos(t *testing.T) {
	zoom, blur := 1.2, 24.0
	items := []capmodel.OverlayItem{
		{ID: "solo", Kind: "image", AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "a"}}},
		{ID: "duo", Kind: "image", AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "a"}, {AssetID: "b"}}, ImageLayers: []capmodel.OverlayImageLayer{{ID: "left"}, {ID: "right"}}},
		{ID: "portraits", Kind: string(capmodel.KindEntityImage), AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "a"}, {AssetID: "b"}}, ImageLayers: []capmodel.OverlayImageLayer{{ID: "left"}, {ID: "right"}}},
		{ID: "chain", Kind: string(capmodel.KindImageChain), Params: map[string]any{
			"chain_segments": []map[string]any{{"kind": "image", "params": map[string]any{}}},
		}},
	}
	radius := 18.0
	canvas := OverlayCanvasSpec{Style: &scriptpkg.OverlayStyleSpec{Image: &scriptpkg.OverlayImageStyleSpec{
		Radius: &radius,
		Backdrop: &scriptpkg.OverlayImageBackdropSpec{Zoom: &zoom, BlurPX: &blur,
			Grid: &scriptpkg.OverlayImageGridSpec{Color: "#7FA8C8"}},
	}}}
	applyPayloadOverlayStyleToItems(items, canvas)
	backdrop, ok := items[0].Params["image_backdrop"].(map[string]any)
	if !ok {
		t.Fatalf("standalone image params = %#v, want image_backdrop", items[0].Params)
	}
	if backdrop["zoom"] != 1.2 || backdrop["blur_px"] != 24.0 {
		t.Fatalf("backdrop = %#v, want zoom 1.2 / blur 24", backdrop)
	}
	if _, ok := items[1].Params["image_backdrop"]; ok {
		t.Fatalf("a generic multi-member composite must not receive image_backdrop: %#v", items[1].Params)
	}
	// Entity portraits are the exception: RenderingGen blurs each member's own
	// photo behind its card, so the item-level block is the transport for a
	// per-member backdrop.
	if _, ok := items[2].Params["image_backdrop"]; !ok {
		t.Fatalf("entity portrait composite must receive image_backdrop: %#v", items[2].Params)
	}
	// A chain owns N independent photos; the run-level treatment is projected
	// onto each image segment, not onto the chain container.
	if _, ok := items[3].Params["image_backdrop"]; ok {
		t.Fatalf("chain container must not carry item-level image_backdrop: %#v", items[3].Params)
	}
	segments, ok := capmodel.ChainSegmentMaps(items[3].Params)
	if !ok || len(segments) != 1 {
		t.Fatalf("chain segments missing: %#v", items[3].Params)
	}
	segmentParams, _ := segments[0]["params"].(map[string]any)
	if _, ok := segmentParams["image_backdrop"]; !ok {
		t.Fatalf("chain image segment must receive image_backdrop: %#v", segments[0])
	}
	if segmentParams["radius"] != 18.0 {
		t.Fatalf("chain image segment must receive the run radius: %#v", segmentParams)
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
	applyPayloadOverlayStyleToItems(items, OverlayCanvasSpec{Style: envelope.Items[0].OverlayStyle})
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
		if !capabilityoverlay.IsTextOverlayKind(kind) {
			t.Errorf("%q should receive runtime text style", kind)
		}
	}
	for _, kind := range []string{"image", "entity_image", "video_overlay"} {
		if capabilityoverlay.IsTextOverlayKind(kind) {
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

// TestImageEffectsAndGridPayloadJSONProjectsToPlan proves the new
// overlay_style.image.effects/grid blocks survive strict payload decoding,
// validation and the projection onto an image item without touching its
// children.
func TestImageEffectsAndGridPayloadJSONProjectsToPlan(t *testing.T) {
	raw := []byte(`{
		"version": 2,
		"preset": "custom",
		"items": [{
			"source": {"type": "text", "topic": "image treatments", "source_text": "Una sola fotografia."},
			"script_params": {"target_words": 100},
			"overlay_style": {"image": {
				"effects": [
					{"type": "glitch", "amount": 0.35, "seed": 11},
					{"type": "vignette", "amount": 0.2, "radius": 0.8, "softness": 0.5}
				],
				"grid": {"scope": "foreground", "style": "lines", "color": "#33CCDD",
					"spacing_px": 72, "line_width_px": 1.25, "opacity": 0.18}
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
	if err := capabilityoverlay.ValidateImageTreatments(envelope.Items[0].OverlayStyle.Image); err != nil {
		t.Fatalf("validate image treatments: %v", err)
	}

	items := []capmodel.OverlayItem{{
		ID: "solo", Kind: "image", AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "a"}},
		Params: map[string]any{},
	}}
	applyPayloadOverlayStyleToItems(items, OverlayCanvasSpec{Style: envelope.Items[0].OverlayStyle})

	effects, ok := items[0].Params["effects"].([]map[string]any)
	if !ok || len(effects) != 2 {
		t.Fatalf("effects not projected: %#v", items[0].Params)
	}
	if effects[0]["type"] != "glitch" || effects[0]["amount"] != 0.35 || effects[0]["seed"] != uint32(11) {
		t.Fatalf("glitch projection = %#v", effects[0])
	}
	if effects[1]["type"] != "vignette" || effects[1]["softness"] != 0.5 {
		t.Fatalf("vignette projection = %#v", effects[1])
	}
	grid, ok := items[0].Params["grid"].(map[string]any)
	if !ok {
		t.Fatalf("grid not projected: %#v", items[0].Params)
	}
	if grid["scope"] != "foreground" || grid["spacing_px"] != 72.0 || grid["line_width_px"] != 1.25 || grid["opacity"] != 0.18 {
		t.Fatalf("grid projection = %#v", grid)
	}
}

// TestImageGridExplicitDisableReachesPlan keeps the explicit off switch
// distinct from an absent block.
func TestImageGridExplicitDisableReachesPlan(t *testing.T) {
	disabled := false
	items := []capmodel.OverlayItem{{
		ID: "solo", Kind: "image", Params: map[string]any{},
		AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "a"}},
	}}
	applyPayloadOverlayStyleToItems(items, OverlayCanvasSpec{Style: &scriptpkg.OverlayStyleSpec{
		Image: &scriptpkg.OverlayImageStyleSpec{Grid: &scriptpkg.OverlayImageGridSpec{Enabled: &disabled}},
	}})
	grid, ok := items[0].Params["grid"].(map[string]any)
	if !ok || grid["enabled"] != false {
		t.Fatalf("explicit grid disable not projected: %#v", items[0].Params)
	}
}
