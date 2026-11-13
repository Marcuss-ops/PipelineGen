package scriptgeneration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	caprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/render"
)

func TestPresentationTypographyUsesRegisteredPlayfairCut(t *testing.T) {
	for _, requested := range []string{"", "playfair_display", "Playfair Display", "playfair_display_italic", "inter"} {
		got := playfairFamily(requested)
		if got != playfairRuntimeFamily {
			t.Fatalf("playfairFamily(%q) = %q, want %q", requested, got, playfairRuntimeFamily)
		}
		if _, ok := caprender.CanonicalFontAssetsByFamily[got]; !ok {
			t.Fatalf("playfairFamily(%q) returned unregistered runtime family %q", requested, got)
		}
	}
}

func TestApplyOverlayPresentationNormalizesChainFontsAndSideCaptions(t *testing.T) {
	item := capmodel.OverlayItem{
		ID: "mixed", Kind: "mixed_chain", CaptionMotionID: "entity_split_side",
		Params: map[string]any{"chain_segments": []any{
			map[string]any{
				"kind": "text", "text": "Testo",
				"params": map[string]any{"font_family": "playfair_display"},
				"style":  map[string]any{"font_family": "inter"},
			},
			map[string]any{
				"kind": "image", "asset_id": "x", "entity_caption": "Francis Kaufmann",
				"caption_layout": "side", "caption_motion_id": "entity_split_side",
				"caption_font_family": "playfair_display",
				"params":              map[string]any{"font_family": "playfair_display"},
			},
		}},
	}
	got := applyOverlayPresentation([]capmodel.OverlayItem{item}, "plan-chain")
	if len(got) != 1 {
		t.Fatalf("chain was rewritten: %+v", got)
	}
	chain := got[0]
	if chain.CaptionLayout != "below" || chain.CaptionFontFamily != playfairRuntimeFamily {
		t.Fatalf("chain caption presentation not normalized: %+v", chain)
	}
	if chain.CaptionMotionID != "" {
		t.Fatalf("side caption motion survived: %q", chain.CaptionMotionID)
	}
	raw, err := json.Marshal(chain.Params)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"font_family":"playfair_display"`) {
		t.Fatalf("stale Playfair alias survived: %s", raw)
	}
	if strings.Contains(string(raw), `"caption_layout":"side"`) {
		t.Fatalf("side caption layout survived: %s", raw)
	}
	if strings.Contains(string(raw), "entity_split_side") {
		t.Fatalf("side caption motion survived: %s", raw)
	}
}

func TestApplyOverlayPresentationNormalizesTypedImageChainCaptionStyle(t *testing.T) {
	// BuildPlan keeps chain segments typed in memory. The old map-only loop
	// skipped this exact representation and left image captions white even
	// though the top-level entity cards were already black with a white glow.
	item := capmodel.OverlayItem{
		ID: "image-chain", Kind: "image_chain",
		Params: map[string]any{"chain_segments": []capmodel.ChainSegment{{
			Kind: "entity_image", AssetID: "ansa", EntityCaption: "ANSA",
			CaptionLayout: "side", CaptionColor: "#FFFFFF",
			Params: map[string]any{"glow_size": 0.0},
			Style:  map[string]any{"fill": "#FFFFFF"},
		}}},
	}
	got := applyOverlayPresentation([]capmodel.OverlayItem{item}, "plan-typed-chain")
	segments, ok := capmodel.DecodeChainSegments(got[0].Params)
	if !ok || len(segments) != 1 {
		t.Fatalf("typed chain segments not preserved: %+v", got[0].Params["chain_segments"])
	}
	segment := segments[0]
	if segment.CaptionLayout != "below" || segment.CaptionColor != presentationTextFill {
		t.Fatalf("caption policy = layout %q color %q, want below/%s", segment.CaptionLayout, segment.CaptionColor, presentationTextFill)
	}
	if segment.Style["fill"] != presentationTextFill {
		t.Fatalf("segment style fill = %v, want %s", segment.Style["fill"], presentationTextFill)
	}
	glow, _ := segment.Style["glow"].(map[string]any)
	if glow["color"] != presentationTextGlowColor {
		t.Fatalf("segment glow = %v, want %s", segment.Style["glow"], presentationTextGlowColor)
	}
	if _, exists := segment.Params["font_family"]; exists {
		t.Fatalf("image segment carries a runtime font: %+v", segment.Params)
	}
}

func TestApplyOverlayPresentationKeepsImageChainContainersImageOnly(t *testing.T) {
	item := capmodel.OverlayItem{
		ID: "image-chain", Kind: "image_chain",
		Params: map[string]any{
			"font_family": "playfair_display", "font_size_px": 42.0,
			"chain_segments": []any{map[string]any{
				"kind": "image", "asset_id": "x", "entity_caption": "Francis Kaufmann",
				"params": map[string]any{"font_family": "playfair_display"},
			}},
		},
	}
	got := applyOverlayPresentation([]capmodel.OverlayItem{item}, "plan-image-chain")
	if len(got) != 1 {
		t.Fatalf("image chain was rewritten: %+v", got)
	}
	chain := got[0]
	if _, exists := chain.Params["font_family"]; exists {
		t.Fatalf("chain container carries text font: %+v", chain.Params)
	}
	segments := chainSegments(chain)
	if len(segments) != 1 {
		t.Fatalf("segments = %d, want 1", len(segments))
	}
	if params, _ := segments[0]["params"].(map[string]any); params != nil {
		if _, exists := params["font_family"]; exists {
			t.Fatalf("image segment carries text font: %+v", params)
		}
	}
	if chain.CaptionLayout != "below" || chain.CaptionFontFamily != playfairRuntimeFamily {
		t.Fatalf("caption policy not applied: %+v", chain)
	}
}

func TestApplyOverlayPresentationBindsVerifiedAssetsToImageEntityCards(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "villa.jpg")
	imageBytes := []byte("verified-villa-park")
	if err := os.WriteFile(imagePath, imageBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(imageBytes)
	sha := hex.EncodeToString(sum[:])
	manifest := map[string]any{
		"version": 1,
		"entities": map[string]any{
			"Villa Pamphili": []any{map[string]any{
				"asset_id": sha, "sha256": sha, "path": "villa.jpg", "media_type": "image/jpeg",
			}},
		},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dir, "entity_asset_registry.json")
	if err := os.WriteFile(manifestPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VELOX_ENTITY_ASSET_REGISTRY", manifestPath)

	items := []capmodel.OverlayItem{
		{ID: "villa-image", SceneID: "discovery", Kind: "entity_card", Text: "Villa Pamphili", ImagePresetID: "image_slide_left"},
		{ID: "villa-text", SceneID: "discovery", Kind: "entity_card", Text: "Villa Pamphili"},
		{ID: "unregistered-image", SceneID: "discovery", Kind: "entity_card", Text: "Unregistered", ImagePresetID: "image_fade_in"},
	}
	got := applyOverlayPresentation(items, "plan-villa")
	if len(got) != 3 {
		t.Fatalf("presentation returned %d items, want 3", len(got))
	}
	if len(got[0].AssetRefs) != 1 || got[0].AssetRefs[0].SHA256 != sha {
		t.Fatalf("image entity card was not bound: %+v", got[0].AssetRefs)
	}
	if got[0].RenderKey != "" {
		t.Fatalf("bound card kept stale render key %q", got[0].RenderKey)
	}
	if len(got[1].AssetRefs) != 0 {
		t.Fatalf("text-only entity card acquired an image: %+v", got[1].AssetRefs)
	}
	if len(got[2].AssetRefs) != 0 {
		t.Fatalf("unregistered entity card acquired an image: %+v", got[2].AssetRefs)
	}
}

func TestApplyOverlayPresentationBindsVerifiedAssetsAndSplitsLongText(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "portrait.jpg")
	imageBytes := []byte("verified-test-portrait")
	if err := os.WriteFile(imagePath, imageBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(imageBytes)
	sha := hex.EncodeToString(sum[:])
	manifest := map[string]any{
		"version": 1,
		"entities": map[string]any{
			"Francis Kaufmann": []any{map[string]any{
				"asset_id": sha, "sha256": sha, "path": "portrait.jpg", "media_type": "image/jpeg",
			}},
		},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dir, "entity_asset_registry.json")
	if err := os.WriteFile(manifestPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VELOX_ENTITY_ASSET_REGISTRY", manifestPath)

	longText := strings.Repeat("Questa frase editoriale deve restare interamente visibile e non può essere tagliata. ", 8)
	items := []capmodel.OverlayItem{
		{
			ID: "francis", Kind: "entity_image", EntityCaption: "Francis Kaufmann",
			CaptionMotionID: "entity_caption_rise", MotionID: "image_focus_reveal",
			AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "wrong", SHA256: "wrong"}},
		},
		{
			ID: "ap", Kind: "entity_image", EntityCaption: "L’Associated Press",
			MotionID: "image_focus_push", CameraMotionID: "image_camera_push",
			EntityStyleID: "random", MotionParams: map[string]any{"enter_frames": 8.0},
			AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "wrong", SHA256: "wrong"}},
		},
		{
			ID: "unregistered-verified", Kind: "entity_image", EntityCaption: "Agency Portrait",
			AssetRefs: []capmodel.OverlayAssetRef{{AssetID: strings.Repeat("a", 64), SHA256: strings.Repeat("a", 64)}},
		},
		{
			ID: "long", Kind: "text_phrase", Text: longText,
			StartUS: 10_000_000, DurationUS: 6_000_000,
			MotionID: "text_word_rise",
		},
	}
	got := applyOverlayPresentation(items, "plan-test")

	var francis, ap *capmodel.OverlayItem
	for index := range got {
		switch got[index].ID {
		case "francis":
			francis = &got[index]
		case "ap":
			ap = &got[index]
		}
	}
	if francis == nil || len(francis.AssetRefs) != 1 || francis.AssetRefs[0].SHA256 != sha {
		t.Fatalf("verified portrait was not bound: %+v", francis)
	}
	if francis.CaptionLayout != "below" || francis.EntityStyleID != capabilityoverlay.IdentityEntityStyleBelowSelector {
		t.Fatalf("entity presentation not normalized: %+v", francis)
	}
	if francis.Params["fill"] != presentationTextFill || francis.Params["glow_color"] != presentationTextGlowColor {
		t.Fatalf("entity typography = fill %v glow %v, want black with a white glow", francis.Params["fill"], francis.Params["glow_color"])
	}
	if _, shadows := francis.Params["shadow_color"]; shadows {
		t.Fatalf("black shadow survived the white-glow treatment: %+v", francis.Params)
	}
	if style, _ := francis.Style["fill"].(string); style != presentationTextFill {
		t.Fatalf("entity style fill = %q, want %q", style, presentationTextFill)
	}
	if francis.CaptionFontFamily != playfairRuntimeFamily {
		t.Fatalf("entity caption font = %q, want %q", francis.CaptionFontFamily, playfairRuntimeFamily)
	}
	if francis.CaptionMotionID != "entity_caption_rise" || francis.MotionID != "image_focus_reveal" {
		t.Fatalf("random motions were overwritten: %+v", francis)
	}
	if ap == nil || ap.Kind != "organization" || len(ap.AssetRefs) != 0 {
		t.Fatalf("unverified entity did not degrade to text: %+v", ap)
	}
	foundVerified := false
	for index := range got {
		if got[index].ID == "unregistered-verified" {
			foundVerified = got[index].Kind == "entity_image" && len(got[index].AssetRefs) == 1
		}
	}
	if !foundVerified {
		t.Fatalf("verified unregistered bundle asset was demoted to text: %+v", got)
	}
	if ap.MotionID == "" || ap.MotionID == "image_focus_push" || ap.MotionParams != nil || ap.CameraMotionID != "" {
		t.Fatalf("text fallback kept image-only motion metadata: %+v", ap)
	}
	chunks := 0
	for _, item := range got {
		if !strings.HasPrefix(item.ID, "long-part-") {
			continue
		}
		chunks++
		if len([]rune(item.Text)) > maxPhraseOverlayRunes {
			t.Fatalf("chunk exceeds phrase limit: %d", len([]rune(item.Text)))
		}
		if item.MotionID != "text_word_rise" {
			t.Fatalf("existing motion was not preserved: %q", item.MotionID)
		}
		if item.StartUS < 10_000_000 || item.EndMs <= item.StartMs {
			t.Fatalf("invalid chunk timing: %+v", item)
		}
	}
	if chunks < 2 {
		t.Fatalf("long phrase produced %d chunks, want at least 2", chunks)
	}
}
