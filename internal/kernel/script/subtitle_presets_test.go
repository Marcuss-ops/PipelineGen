package script

import "testing"

func TestVideoRenderSpecNormalizeResolvesSubtitlePreset(t *testing.T) {
	render := VideoRenderSpec{
		Subtitles: &VideoSubtitlesSpec{Enabled: true, Preset: "tiktok"},
		SubtitlePresets: map[string]VideoVisualStyleSpec{
			"tiktok": {Font: "Montserrat", Size: 58, Position: "bottom_center"},
		},
	}
	render.Normalize()
	if render.Subtitles.Style == nil {
		t.Fatal("subtitle preset was not resolved")
	}
	if render.Subtitles.Style.Font != "Montserrat" || render.Subtitles.Style.Position != "bottom_center" || render.Subtitles.Style.FontSizePX != 58 {
		t.Fatalf("resolved subtitle style = %+v", render.Subtitles.Style)
	}
}

// TestVideoRenderSpecNormalizeResolvesYoungSubtitleFamily pins the "Young"
// subtitle family: each canonical preset id resolves through the shared table
// to the typography that the ASS owner
// (assets/texttracks/ass_materializer.go::ResolveFontPreset) burns for the
// same style id, so the overlay size and the burnt captions cannot drift.
func TestVideoRenderSpecNormalizeResolvesYoungSubtitleFamily(t *testing.T) {
	tests := []struct {
		preset       string
		wantFont     string
		wantSize     float64
		wantPosition string
	}{
		{"subs-young", "Poppins", 60, "bottom_center"},
		{"subs-young-pop", "Poppins", 64, "bottom_center"},
		{"subs-young-clean", "Montserrat", 56, "bottom_center"},
		{"subs-young-center", "Poppins", 60, "middle_center"},
	}
	seen := map[VideoVisualStyleSpec]string{}
	for _, tc := range tests {
		render := VideoRenderSpec{Subtitles: &VideoSubtitlesSpec{Enabled: true, Preset: tc.preset}}
		render.Normalize()
		if render.Subtitles.Style == nil {
			t.Fatalf("%s: subtitle preset was not resolved", tc.preset)
		}
		style := *render.Subtitles.Style
		if style.Font != tc.wantFont || style.FontSizePX != tc.wantSize || style.Position != tc.wantPosition {
			t.Errorf("%s: resolved style = %+v; want font=%s size=%v position=%s",
				tc.preset, style, tc.wantFont, tc.wantSize, tc.wantPosition)
		}
		if prev, ok := seen[style]; ok {
			t.Errorf("%s and %s resolve to the identical subtitle style %+v", prev, tc.preset, style)
		}
		seen[style] = tc.preset
	}

	// style_id is the alias used by clip.render payloads and must resolve
	// exactly like preset.
	viaStyleID := VideoRenderSpec{Subtitles: &VideoSubtitlesSpec{Enabled: true, StyleID: "subs-young"}}
	viaStyleID.Normalize()
	if viaStyleID.Subtitles.Style == nil || viaStyleID.Subtitles.Style.Font != "Poppins" || viaStyleID.Subtitles.Style.FontSizePX != 60 {
		t.Fatalf("style_id subs-young resolved = %+v", viaStyleID.Subtitles.Style)
	}
}
