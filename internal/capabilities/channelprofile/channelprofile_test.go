package channelprofile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		OverlayStyle: &OverlayStyleProfile{
			Color:        []float64{1, 1, 1, 1},
			Shadow:       &OverlayShadowProfile{Enabled: boolPtr(true), Color: "#000000", Opacity: floatPtr(0.72), Blur: floatPtr(12), Offset: []float64{0, 4}},
			TransitionIn: &OverlayTransitionProfile{Preset: "fade_in", DurationFrames: 8},
		},
		SoundEffects: []SoundEffectProfile{{AssetID: "whoosh1", AtMS: 0, GainDB: &gain}},
		MixPolicy:    "VOICEOVER_DUCKED_CLIP",
		PhraseMotions: []string{
			"slide_up", "fade_in",
		},
	}
}

func boolPtr(v bool) *bool        { return &v }
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
		{"transition without preset", func(p *Profile) { p.OverlayStyle.TransitionIn = &OverlayTransitionProfile{DurationFrames: 4} }, "preset"},
		{"sfx without asset", func(p *Profile) { p.SoundEffects = []SoundEffectProfile{{AtMS: 10}} }, "asset_id"},
		{"boosting sfx gain", func(p *Profile) { p.SoundEffects = []SoundEffectProfile{{AssetID: "x", GainDB: &boost}} }, "gain_db"},
		{"absolute and scene sfx placement", func(p *Profile) { p.SoundEffects = []SoundEffectProfile{{AssetID: "x", AtMS: 10, SceneID: "scene-1"}} }, "both"},
		{"unknown mix policy", func(p *Profile) { p.MixPolicy = "duck_everything" }, "mix_policy"},
		{"uncertified motion", func(p *Profile) { p.PhraseMotions = []string{"not_a_motion"} }, "certified"},
		{"duplicate motion", func(p *Profile) { p.PhraseMotions = []string{"slide_up", "slide_up"} }, "repeats"},
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
