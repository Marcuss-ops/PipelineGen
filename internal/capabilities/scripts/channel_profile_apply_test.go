package scriptgeneration

import (
	"encoding/json"
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/channelprofile"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// renderRequest is a request that already opted into a render but declared
// none of the surfaces a channel profile may complete.
func renderRequest() GenerateRequest {
	return GenerateRequest{
		IdempotencyKey: "channel-key",
		Audio:          capabilityaudio.AudioModeCombinedTimeline,
		Render:         scriptpkg.VideoRenderSpec{Enabled: true},
	}
}

func TestApplyChannelProfileFillsBlankSurfaces(t *testing.T) {
	gain := -18.0
	profile := channelprofile.Profile{
		ChannelID: "crime",
		Subtitles: &channelprofile.SubtitlesProfile{Preset: "impact"},
		Watermark: &channelprofile.WatermarkProfile{Text: "CRIME FILES", Position: "top_right", MarginPX: 48},
		OverlayStyle: &channelprofile.OverlayStyleProfile{
			TransitionIn: &channelprofile.OverlayTransitionProfile{Preset: "fade_in", DurationFrames: 8},
		},
		SoundEffects:  []channelprofile.SoundEffectProfile{{AssetID: "whoosh1", GainDB: &gain}},
		MixPolicy:     "VOICEOVER_DUCKED_CLIP",
		PhraseMotions: []string{"masked_upward_reveal"},
	}
	req := renderRequest()
	if err := ApplyChannelProfile(&req, profile); err != nil {
		t.Fatalf("ApplyChannelProfile: %v", err)
	}

	if req.Render.Subtitles == nil || !req.Render.Subtitles.Enabled {
		t.Fatalf("subtitles not filled: %+v", req.Render.Subtitles)
	}
	if req.Render.Subtitles.Style == nil || req.Render.Subtitles.Style.Font != "Impact" || req.Render.Subtitles.Style.FontSizePX != 58 {
		t.Fatalf("preset not projected: %+v", req.Render.Subtitles.Style)
	}
	// Normalize must have completed the canonical defaults a burn-mode plan
	// needs (the renderer fails closed without color/position).
	if req.Render.Subtitles.Style.Color == "" || req.Render.Subtitles.Style.Position == "" {
		t.Fatalf("canonical subtitle defaults not filled: %+v", req.Render.Subtitles.Style)
	}
	if req.Render.Subtitles.StyleID != "impact" {
		t.Fatalf("StyleID = %q, want the preset id (the ASS burn path resolves by style id)", req.Render.Subtitles.StyleID)
	}
	if req.Render.Watermark == nil || !req.Render.Watermark.Enabled || req.Render.Watermark.Text != "CRIME FILES" {
		t.Fatalf("watermark not filled: %+v", req.Render.Watermark)
	}
	if req.Render.Watermark.Style == nil || req.Render.Watermark.Style.FontSizePX == 0 {
		t.Fatalf("watermark style has no size: %+v", req.Render.Watermark.Style)
	}
	if req.OverlayStyle == nil || req.OverlayStyle.TransitionIn == nil || req.OverlayStyle.TransitionIn.Preset != "fade_in" {
		t.Fatalf("overlay style not filled: %+v", req.OverlayStyle)
	}
	if len(req.SoundEffects) != 1 || req.SoundEffects[0].AssetID != "whoosh1" || req.SoundEffects[0].GainDB != -18 {
		t.Fatalf("sfx not filled: %+v", req.SoundEffects)
	}
	if req.MixPolicy != capabilityaudio.MixVoiceoverWithDuckedClip {
		t.Fatalf("mix policy = %q, want %q", req.MixPolicy, capabilityaudio.MixVoiceoverWithDuckedClip)
	}
	if len(req.PhraseMotions) != 1 || req.PhraseMotions[0] != "masked_upward_reveal" {
		t.Fatalf("phrase motions not filled: %+v", req.PhraseMotions)
	}
}

// The guardrail that keeps a profile a default-fill and never an override.
func TestApplyChannelProfileNeverOverridesCallerIntent(t *testing.T) {
	profile := channelprofile.Profile{
		ChannelID:     "crime",
		Subtitles:     &channelprofile.SubtitlesProfile{Preset: "impact"},
		Watermark:     &channelprofile.WatermarkProfile{Text: "CHANNEL BRAND"},
		OverlayStyle:  &channelprofile.OverlayStyleProfile{Color: []float64{1, 0, 0, 1}},
		SoundEffects:  []channelprofile.SoundEffectProfile{{AssetID: "channel-whoosh"}},
		MixPolicy:     "VOICEOVER_ONLY",
		PhraseMotions: []string{"kinetic_split_word"},
	}
	req := renderRequest()
	req.Render.Subtitles = &scriptpkg.VideoSubtitlesSpec{
		Enabled: true, Preset: "montserrat", StyleID: "montserrat",
		Style: &scriptpkg.VideoVisualStyleSpec{Font: "Montserrat", FontSizePX: 54, Color: "#FFFFFF", Position: "bottom_center"},
	}
	req.Render.Watermark = &scriptpkg.VideoWatermarkSpec{Enabled: true, Text: "CALLER", Position: "top_left", MarginPX: 40}
	req.OverlayStyle = &scriptpkg.OverlayStyleSpec{Color: []float64{0, 1, 0, 1}}
	req.SoundEffects = []scriptpkg.SoundEffectIntent{{AssetID: "caller-whoosh", AtMS: 500}}
	req.MixPolicy = capabilityaudio.MixVoiceoverOnly
	req.PhraseMotions = []string{"velocity_inertia_snap"}

	if err := ApplyChannelProfile(&req, profile); err != nil {
		t.Fatalf("ApplyChannelProfile: %v", err)
	}
	if req.Render.Subtitles.Preset != "montserrat" || req.Render.Subtitles.Style.Font != "Montserrat" {
		t.Fatalf("caller subtitles overridden: %+v", req.Render.Subtitles)
	}
	if req.Render.Watermark.Text != "CALLER" || req.Render.Watermark.Position != "top_left" {
		t.Fatalf("caller watermark overridden: %+v", req.Render.Watermark)
	}
	if req.OverlayStyle.Color[1] != 1 {
		t.Fatalf("caller overlay style overridden: %+v", req.OverlayStyle)
	}
	if len(req.SoundEffects) != 1 || req.SoundEffects[0].AssetID != "caller-whoosh" {
		t.Fatalf("caller sfx overridden: %+v", req.SoundEffects)
	}
	if req.MixPolicy != capabilityaudio.MixVoiceoverOnly {
		t.Fatalf("caller mix policy overridden: %q", req.MixPolicy)
	}
	if len(req.PhraseMotions) != 1 || req.PhraseMotions[0] != "velocity_inertia_snap" {
		t.Fatalf("caller motion pool overridden: %+v", req.PhraseMotions)
	}
}

// A profile completes a feature the caller opted into — it never opts in for
// them (the same gate ApplyEditingAssetPolicy applies to the overlay
// background).
func TestApplyChannelProfileIsGatedOnRenderAndAudioMode(t *testing.T) {
	profile := channelprofile.Profile{
		ChannelID:    "crime",
		Subtitles:    &channelprofile.SubtitlesProfile{Preset: "impact"},
		Watermark:    &channelprofile.WatermarkProfile{Text: "BRAND"},
		SoundEffects: []channelprofile.SoundEffectProfile{{AssetID: "whoosh1"}},
		MixPolicy:    "VOICEOVER_ONLY",
	}
	t.Run("no render requested", func(t *testing.T) {
		req := GenerateRequest{Audio: capabilityaudio.AudioModeCombinedTimeline}
		if err := ApplyChannelProfile(&req, profile); err != nil {
			t.Fatal(err)
		}
		if req.Render.Subtitles != nil || req.Render.Watermark != nil {
			t.Fatalf("visual profile applied without a render: %+v", req.Render)
		}
	})
	t.Run("audio layers outside COMBINED_TIMELINE", func(t *testing.T) {
		for _, mode := range []capabilityaudio.AudioMode{capabilityaudio.AudioModeNone, capabilityaudio.AudioModeChunkedVoiceover, ""} {
			req := GenerateRequest{Audio: mode}
			if err := ApplyChannelProfile(&req, profile); err != nil {
				t.Fatal(err)
			}
			if len(req.SoundEffects) != 0 || req.MixPolicy != "" {
				t.Fatalf("audio profile applied on mode %q: sfx=%+v policy=%q", mode, req.SoundEffects, req.MixPolicy)
			}
		}
	})
	t.Run("disabled subtitles stay disabled", func(t *testing.T) {
		off := profile
		off.Subtitles = &channelprofile.SubtitlesProfile{Preset: "impact", Enabled: boolRef(false)}
		req := renderRequest()
		if err := ApplyChannelProfile(&req, off); err != nil {
			t.Fatal(err)
		}
		if req.Render.Subtitles != nil {
			t.Fatalf("enabled:false profile injected subtitles: %+v", req.Render.Subtitles)
		}
	})
}

func TestApplyChannelProfileRejectsNilRequestAndInvalidProfile(t *testing.T) {
	if err := ApplyChannelProfile(nil, channelprofile.Profile{ChannelID: "x"}); err == nil {
		t.Fatal("nil request must fail closed")
	}
	req := renderRequest()
	if err := ApplyChannelProfile(&req, channelprofile.Profile{ChannelID: "x", MixPolicy: "bogus"}); err == nil {
		t.Fatal("an invalid profile must fail closed")
	}
}

func boolRef(v bool) *bool { return &v }

// TestBuildGenerateRequestAppliesTheChannelProfile is the end-to-end half: an
// envelope that DECLARES channel_id gets the profile's blank-fills at the
// single ingress both job handlers share, while an envelope that does not —
// or that carries its own values — is untouched.
func TestBuildGenerateRequestAppliesTheChannelProfile(t *testing.T) {
	channelprofile.Install([]channelprofile.Profile{{
		ChannelID:     "crime",
		Subtitles:     &channelprofile.SubtitlesProfile{Preset: "impact"},
		Watermark:     &channelprofile.WatermarkProfile{Text: "CRIME FILES", Position: "top_right", MarginPX: 48},
		PhraseMotions: []string{"masked_upward_reveal"},
	}})
	defer channelprofile.Reset()

	decode := func(payload string) *scriptpkg.GenerationEnvelopeV2 {
		t.Helper()
		var env scriptpkg.GenerationEnvelopeV2
		if err := json.Unmarshal([]byte(payload), &env); err != nil {
			t.Fatal(err)
		}
		return &env
	}
	clipScript := func(itemExtra, renderExtra string) string {
		// channel_id is an ITEM field; every style choice lives inside
		// output.render (the envelope's only render block), which is where the
		// builder reads it from.
		return `{"version":2,"items":[{"title":"channel-profile","project":"test-project","language":"en",` +
			`"source":{"type":"clips","clip_ids":["clip-a"]},` +
			`"output":{"voiceover_enabled":true,"render":{"enabled":true` + renderExtra + `}},` +
			`"audio":{"mode":"COMBINED_TIMELINE"}` + itemExtra + `}]}`
	}

	t.Run("a declared channel receives its profile", func(t *testing.T) {
		got, err := BuildGenerateRequest(decode(clipScript(`,"channel_id":"crime"`, "")), "channel-profile-key")
		if err != nil {
			t.Fatal(err)
		}
		if got.ChannelID != "crime" {
			t.Fatalf("ChannelID = %q, want the envelope value carried verbatim", got.ChannelID)
		}
		if got.Render.Subtitles == nil || got.Render.Subtitles.Preset != "impact" {
			t.Fatalf("channel subtitles not applied: %+v", got.Render.Subtitles)
		}
		if got.Render.Watermark == nil || got.Render.Watermark.Text != "CRIME FILES" {
			t.Fatalf("channel watermark not applied: %+v", got.Render.Watermark)
		}
		if len(got.PhraseMotions) != 1 || got.PhraseMotions[0] != "masked_upward_reveal" {
			t.Fatalf("channel motion pool not applied: %+v", got.PhraseMotions)
		}
	})

	t.Run("an undeclared channel is untouched", func(t *testing.T) {
		got, err := BuildGenerateRequest(decode(clipScript("", "")), "channel-profile-key")
		if err != nil {
			t.Fatal(err)
		}
		if got.ChannelID != "" || got.Render.Subtitles != nil || got.Render.Watermark != nil || len(got.PhraseMotions) != 0 {
			t.Fatalf("a profile leaked onto a request that declared no channel: %+v", got)
		}
	})

	t.Run("an unknown channel is a no-op, not an error", func(t *testing.T) {
		if _, err := BuildGenerateRequest(decode(clipScript(`,"channel_id":"no-such-channel"`, "")), "channel-profile-key"); err != nil {
			t.Fatalf("an unprofiled channel must build cleanly: %v", err)
		}
	})

	t.Run("explicit caller subtitles survive the profile", func(t *testing.T) {
		callerSubs := `,"subtitles":{"enabled":true,"preset":"montserrat","style":{"font":"Montserrat","color":"#FFFFFF","font_size_px":54,"position":"bottom_center"}}`
		got, err := BuildGenerateRequest(decode(clipScript(`,"channel_id":"crime"`, callerSubs)), "channel-profile-key")
		if err != nil {
			t.Fatal(err)
		}
		if got.Render.Subtitles == nil || got.Render.Subtitles.Preset != "montserrat" {
			t.Fatalf("caller subtitles were overridden by the channel profile: %+v", got.Render.Subtitles)
		}
	})
}
