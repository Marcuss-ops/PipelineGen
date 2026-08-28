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
