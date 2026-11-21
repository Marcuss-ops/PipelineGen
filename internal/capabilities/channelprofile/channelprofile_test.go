package channelprofile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func validProfile() Profile {
	gain := -18.0
	return Profile{
		ChannelID: "crime",
		Subtitles: &SubtitlesProfile{
			Preset: "impact",
			Style:  &StyleProfile{Color: "#F5F5F5", Position: "bottom_center"},
		},
		Watermark: &WatermarkProfile{
			Text: "CRIME FILES", Position: "top_right", Opacity: 0.85, MarginPX: 48,
			Style: &StyleProfile{Font: "Montserrat", Color: "#FFFFFF", FontSizePX: 42},
		},
		OverlayStyle: &scriptpkg.OverlayStyleSpec{
			Color:        []float64{1, 1, 1, 1},
			Shadow:       &scriptpkg.OverlayShadowSpec{Enabled: true, Color: "#000000", Opacity: floatPtr(0.72), Blur: floatPtr(12), Offset: []float64{0, 4}},
			TransitionIn: &scriptpkg.OverlayTransitionSpec{Preset: "fade_in", DurationFrames: 8},
		},
		SoundEffects:  []SoundEffectProfile{{AssetID: "whoosh1", AtMS: 0, GainDB: &gain}},
		MixPolicy:     "VOICEOVER_DUCKED_CLIP",
		ImageMotions:  []string{"image_25d_card_swing", "image_depth_dolly"},
		EntityStyleID: "badge",
	}
}

func floatPtr(v float64) *float64 { return &v }

func TestValidateAcceptsAWellFormedProfile(t *testing.T) {
	if err := validProfile().Validate(); err != nil {
		t.Fatalf("valid profile rejected: %v", err)
	}
}

func TestValidateFailsClosed(t *testing.T) {
	boost := 3.0
	for _, tc := range []struct {
		name   string
		mutate func(*Profile)
		want   string
	}{
		{"missing channel id", func(p *Profile) { p.ChannelID = " " }, "channel_id"},
		{"unknown subtitle preset", func(p *Profile) { p.Subtitles.Preset = "nope" }, "preset"},
		{"subtitles without size or preset", func(p *Profile) { p.Subtitles.Preset = ""; p.Subtitles.Style = nil }, "font size"},
		{"bad subtitle position", func(p *Profile) { p.Subtitles.Style.Position = "bottom_left" }, "position"},
		{"watermark without text", func(p *Profile) { p.Watermark.Text = " " }, "text"},
		{"bad watermark position", func(p *Profile) { p.Watermark.Position = "bottom_center" }, "position"},
		{"watermark opacity out of range", func(p *Profile) { p.Watermark.Opacity = 1.5 }, "opacity"},
		{"overlay color arity", func(p *Profile) { p.OverlayStyle.Color = []float64{1, 1} }, "RGBA"},
		{"transition without preset", func(p *Profile) { p.OverlayStyle.TransitionIn = &scriptpkg.OverlayTransitionSpec{DurationFrames: 4} }, "preset"},
		{"sfx without asset", func(p *Profile) { p.SoundEffects = []SoundEffectProfile{{AtMS: 10}} }, "asset_id"},
		{"boosting sfx gain", func(p *Profile) { p.SoundEffects = []SoundEffectProfile{{AssetID: "x", GainDB: &boost}} }, "gain_db"},
		{"absolute and scene sfx placement", func(p *Profile) { p.SoundEffects = []SoundEffectProfile{{AssetID: "x", AtMS: 10, SceneID: "scene-1"}} }, "both"},
		{"unknown mix policy", func(p *Profile) { p.MixPolicy = "duck_everything" }, "mix_policy"},
		{"uncertified image motion", func(p *Profile) { p.ImageMotions = []string{"not_an_image_motion"} }, "certified image motion"},
		{"duplicate image motion", func(p *Profile) { p.ImageMotions = []string{"image_25d_card_swing", "image_25d_card_swing"} }, "repeats"},
		// image_card_push is now part of the certified 32-motion render-safe
		// catalog, so the fail-closed case uses an id outside that catalog.
		{"retired image motion", func(p *Profile) { p.ImageMotions = []string{"image_card_push_legacy"} }, "certified image motion"},
		{"unknown entity style selector", func(p *Profile) { p.EntityStyleID = "neon_explosion" }, "entity_style_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := validProfile()
			tc.mutate(&p)
			err := p.Validate()
			if err == nil {
				t.Fatal("expected a fail-closed error, got nil")
			}
			if want := tc.want; !contains(err.Error(), want) {
				t.Fatalf("error %q does not mention %q", err, want)
			}
		})
	}
}

func TestParseRejectsBadDocuments(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  string
	}{
		{"not yaml", "version: 1\nprofiles: ["},
		{"wrong version", "version: 99\nprofiles: []\n"},
		{"duplicate channel", "version: 1\nprofiles:\n  - channel_id: a\n  - channel_id: a\n"},
		{"invalid profile", "version: 1\nprofiles:\n  - channel_id: a\n    mix_policy: bogus\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse([]byte(tc.doc)); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestParseAndInstallRoundTrip(t *testing.T) {
	doc := "version: 1\nprofiles:\n  - channel_id: young\n    subtitles:\n      preset: subs-young-pop\n      style:\n        position: bottom_center\n"
	profiles, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	Install(profiles)
	defer Reset()

	p, ok := Lookup("young")
	if !ok || p.ChannelID != "young" || p.Subtitles == nil || p.Subtitles.Preset != "subs-young-pop" {
		t.Fatalf("Lookup(young) = %+v, ok=%v", p, ok)
	}
	if _, ok := Lookup(""); ok {
		t.Fatal("an empty channel id must not match any profile")
	}
	if _, ok := Lookup("unknown-channel"); ok {
		t.Fatal("an unknown channel must not match (no default installed)")
	}
}

// TestParseDecodesTheCanonicalOverlayStyleSpec is the anti-drift guard the
// deleted OverlayStyleProfile mirror made necessary: a profile document feeds
// the canonical kernel spec directly, so every field the kernel publishes —
// including the five (Bullet, GlowColor, Cursor, Image, CardBackground) the
// mirror silently dropped — must arrive on the decoded value.
func TestParseDecodesTheCanonicalOverlayStyleSpec(t *testing.T) {
	doc := `version: 1
profiles:
  - channel_id: full
    overlay_style:
      color: [1.0, 0.5, 0.0, 1.0]
      font_family: oswald
      glow_size: 42
      glow_color: "#00FF88"
      stroke_size: 3.5
      size:
        width: 640
        height: 120
        font_size: 58
      shadow:
        enabled: true
        color: "#000000"
        opacity: 0.6
        blur: 12
        offset: [0, 4]
      transition_in:
        preset: fade_in
        duration_frames: 6
      bullet:
        font_family: inter_bold
        font_size_px: 44
        glow_size: 0
        glow_color: "#FFEE00"
        stroke_size: 2
      cursor:
        color: "#FFFFFF"
        width: 6
        height: 72
        blink: true
      image:
        width: 1340
        height: 750
        position_y: -120
        radius: 32
        fit: cover
        bounded: true
        border: {width: 4, color: "#FFFFFF", radius: 18}
        stroke: {width: 2, color: "#000000"}
        glow: {radius: 24, intensity: 0.3, color: "#FFFFFF"}
        shadow: {enabled: true, color: "#000000", opacity: 0.5, blur: 12, offset: [0, 6]}
        effects:
          - type: none
        grid: {enabled: true, scope: foreground, style: dots, color: "#204060", spacing_px: 48, dot_radius_px: 2, opacity: 0.2}
        backdrop: {zoom: 1.4, blur_px: 18, grid: {enabled: true, style: lines, line_width_px: 1, opacity: 0.1}}
      card_background:
        color: "#101010"
        opacity: 0.8
        radius: 24
        padding: [12, 18]
        stroke: {color: "#FFFFFF", width: 2}
        shadow: {color: "#000000", opacity: 0.4, blur: 10, offset: [0, 4]}
        motions: [fade, wipe_right]
`
	profiles, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	style := profiles[0].OverlayStyle
	if style == nil {
		t.Fatal("overlay_style did not decode into the canonical spec")
	}
	if style.FontFamily != "oswald" || style.GlowColor != "#00FF88" ||
		style.GlowSize == nil || *style.GlowSize != 42 ||
		style.StrokeSize == nil || *style.StrokeSize != 3.5 {
		t.Fatalf("run-level typography lost: %+v", style)
	}
	if len(style.Color) != 4 || style.Color[1] != 0.5 {
		t.Fatalf("color lost: %+v", style.Color)
	}
	if style.Size == nil || style.Size.Width == nil || *style.Size.Width != 640 ||
		style.Size.Height == nil || *style.Size.Height != 120 ||
		style.Size.FontSize == nil || *style.Size.FontSize != 58 {
		t.Fatalf("size lost: %+v", style.Size)
	}
	if style.Shadow == nil || !style.Shadow.Enabled || style.Shadow.Opacity == nil || *style.Shadow.Opacity != 0.6 || len(style.Shadow.Offset) != 2 {
		t.Fatalf("shadow lost: %+v", style.Shadow)
	}
	if style.TransitionIn == nil || style.TransitionIn.Preset != "fade_in" || style.TransitionIn.DurationFrames != 6 {
		t.Fatalf("transition_in lost: %+v", style.TransitionIn)
	}
	if style.Bullet == nil || style.Bullet.FontFamily != "inter_bold" ||
		style.Bullet.FontSizePX == nil || *style.Bullet.FontSizePX != 44 ||
		style.Bullet.GlowSize == nil || *style.Bullet.GlowSize != 0 ||
		style.Bullet.GlowColor != "#FFEE00" || style.Bullet.StrokeSize == nil || *style.Bullet.StrokeSize != 2 {
		t.Fatalf("bullet lost: %+v", style.Bullet)
	}
	if style.Cursor == nil || style.Cursor.Color != "#FFFFFF" ||
		style.Cursor.Width == nil || *style.Cursor.Width != 6 ||
		style.Cursor.Height == nil || *style.Cursor.Height != 72 ||
		style.Cursor.Blink == nil || !*style.Cursor.Blink {
		t.Fatalf("cursor lost: %+v", style.Cursor)
	}
	if style.Image == nil || style.Image.Fit != "cover" ||
		style.Image.Width == nil || *style.Image.Width != 1340 ||
		style.Image.PositionY == nil || *style.Image.PositionY != -120 ||
		style.Image.Border == nil || style.Image.Border.Radius == nil || *style.Image.Border.Radius != 18 ||
		style.Image.Glow == nil || style.Image.Glow.Intensity == nil || *style.Image.Glow.Intensity != 0.3 ||
		style.Image.Shadow == nil || !style.Image.Shadow.Enabled ||
		len(style.Image.Effects) != 1 || style.Image.Effects[0].Type != "none" ||
		style.Image.Grid == nil || style.Image.Grid.Style != "dots" ||
		style.Image.Backdrop == nil || style.Image.Backdrop.Grid == nil || style.Image.Backdrop.Grid.Style != "lines" {
		t.Fatalf("image lost: %+v", style.Image)
	}
	if style.CardBackground == nil || style.CardBackground.Color != "#101010" ||
		style.CardBackground.Opacity == nil || *style.CardBackground.Opacity != 0.8 ||
		len(style.CardBackground.Padding) != 2 || style.CardBackground.Stroke == nil ||
		style.CardBackground.Shadow == nil || len(style.CardBackground.Motions) != 2 {
		t.Fatalf("card_background lost: %+v", style.CardBackground)
	}
}

// TestParseAppliesTheProfileShadowDefault pins the one rule the canonical
// wire type cannot express: a declared shadow block is enabled by intent,
// while an explicit enabled:false keeps it off.
func TestParseAppliesTheProfileShadowDefault(t *testing.T) {
	doc := `version: 1
profiles:
  - channel_id: implicit
    overlay_style:
      shadow:
        color: "#000000"
        blur: 10
  - channel_id: explicit-on
    overlay_style:
      shadow:
        enabled: true
        blur: 10
  - channel_id: explicit-off
    overlay_style:
      shadow:
        enabled: false
        blur: 10
`
	profiles, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	for i, want := range []bool{true, true, false} {
		shadow := profiles[i].OverlayStyle.Shadow
		if shadow == nil {
			t.Fatalf("profiles[%d]: shadow block did not decode", i)
		}
		if shadow.Enabled != want {
			t.Fatalf("profiles[%d] (%s): shadow.enabled = %v, want %v", i, profiles[i].ChannelID, shadow.Enabled, want)
		}
	}
}

func TestLookupFallsBackToTheReservedDefault(t *testing.T) {
	profiles, err := Parse([]byte("version: 1\nprofiles:\n  - channel_id: default\n    subtitles:\n      preset: montserrat\n      style:\n        position: bottom_center\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	Install(profiles)
	defer Reset()

	p, ok := Lookup("UC-ANY-CHANNEL")
	if !ok || p.Subtitles.Preset != "montserrat" {
		t.Fatalf("default fallback did not apply: %+v ok=%v", p, ok)
	}
}

func TestLoadOptionalToleratesAbsenceButFailsOnBrokenFile(t *testing.T) {
	Reset()
	dir := t.TempDir()
	missing := filepath.Join(dir, "absent.yaml")
	if found, err := LoadOptional(missing); err != nil || found {
		t.Fatalf("LoadOptional(missing) = %v, %v; want false, nil", found, err)
	}

	broken := filepath.Join(dir, "broken.yaml")
	if err := os.WriteFile(broken, []byte("version: 1\nprofiles:\n  - channel_id: a\n    mix_policy: bogus\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOptional(broken); err == nil {
		t.Fatal("a present-but-invalid document must fail closed")
	}
}

func TestShippedChannelProfilesDocumentParses(t *testing.T) {
	// The checked-in seed is the operator's starting point; if it drifts from
	// the schema the next boot would take the whole server down, so the drift
	// must be visible here instead.
	Reset()
	defer Reset()
	found, err := LoadOptional(filepath.Join("..", "..", "..", "config", "channel_profiles.yaml"))
	if err != nil {
		t.Fatalf("shipped config/channel_profiles.yaml does not parse: %v", err)
	}
	if !found {
		t.Skip("config/channel_profiles.yaml not present in this checkout")
	}
	if len(Snapshot()) == 0 {
		t.Fatal("shipped document installs zero profiles")
	}
}

func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
