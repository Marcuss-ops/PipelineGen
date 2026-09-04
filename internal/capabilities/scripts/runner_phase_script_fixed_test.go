package scriptgeneration

import (
	"strings"
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestApplyFixedSectionsStampsExplicitRoles certifies that injected fixed
// sections carry the explicit SceneRole (opening/closing) alongside their
// ExecutionMode, so projections and documents derive section semantics from
// Role — never from the scene ID.
func TestApplyFixedSectionsStampsExplicitRoles(t *testing.T) {
	req := GenerateRequest{
		SourceLanguage: "en",
		Intro: &scriptpkg.FixedSection{
			ClipIDs:     []string{"intro-a", "intro-b"},
			DisplayText: "Welcome",
			Playback:    scriptpkg.FixedPlaybackPolicy{AudioMode: scriptpkg.FixedPlaybackOriginalClip, SourceInMS: 0, SourceOutMS: 4_000},
		},
		Outro: &scriptpkg.FixedSection{
			ClipIDs:     []string{"outro-a"},
			DisplayText: "See you next time",
			Playback:    scriptpkg.FixedPlaybackPolicy{AudioMode: scriptpkg.FixedPlaybackOriginalClip, SourceInMS: 0, SourceOutMS: 2_000},
		},
	}
	scenes, err := applyFixedSections(req, []Scene{{
		ID: "body", Index: 0, DurationUS: 5_000_000, Text: map[Language]string{"en": "BODY"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(scenes) != 3 {
		t.Fatalf("scene count = %d, want 3 (intro + body + outro)", len(scenes))
	}
	intro := scenes[0]
	if !intro.ExecutionMode.IsFixedMedia() || intro.Role != scriptpkg.SceneRoleOpening {
		t.Fatalf("intro scene = mode %q role %q, want fixed_media/opening", intro.ExecutionMode, intro.Role)
	}
	if intro.ID != "scene-intro" || intro.Index != 0 {
		t.Fatalf("intro identity = %q@%d, want scene-intro@0", intro.ID, intro.Index)
	}
	if len(intro.Clips) != 2 || intro.Clip == nil || intro.Clip.ID != "intro-a" {
		t.Fatalf("intro clips = %d (primary %v), want both intro clips", len(intro.Clips), intro.Clip)
	}
	if text := strings.TrimSpace(intro.Text[req.SourceLanguage]); text != "Welcome" {
		t.Fatalf("intro text = %q, want display text only", text)
	}
	body := scenes[1]
	if body.ExecutionMode.IsFixedMedia() || body.ID != "body" {
		t.Fatalf("body scene mutated: mode %q id %q", body.ExecutionMode, body.ID)
	}
	outro := scenes[2]
	if !outro.ExecutionMode.IsFixedMedia() || outro.Role != scriptpkg.SceneRoleClosing {
		t.Fatalf("outro scene = mode %q role %q, want fixed_media/closing", outro.ExecutionMode, outro.Role)
	}
	if outro.ID != "scene-outro" || outro.Index != 2 {
		t.Fatalf("outro identity = %q@%d, want scene-outro@2", outro.ID, outro.Index)
	}
}

// TestRenderUnitsForSceneFansTwoClipFixedSections certifies the canonical
// render-unit decomposition: a 2-clip fixed intro/outro produces two render
// units (one per clip) so the second clip receives its own final render,
// while a generated scene stays a single unit on its primary clip.
func TestRenderUnitsForSceneFansTwoClipFixedSections(t *testing.T) {
	fixed := Scene{
		ID: "scene-fixed-01", Index: 0,
		Role:          scriptpkg.SceneRoleOpening,
		ExecutionMode: scriptpkg.SceneExecutionFixedMedia,
		Clip:          &ClipReference{ID: "intro-a", DurationUS: 4_000_000},
		Clips: []*ClipReference{
			{ID: "intro-a", DurationUS: 4_000_000},
			{ID: "intro-b", DurationUS: 4_000_000},
		},
	}
	units := RenderUnitsForScene(fixed)
	if len(units) != 2 {
		t.Fatalf("fixed 2-clip units = %d, want 2", len(units))
	}
	if units[0].ClipIndex != 0 || units[0].Clip.ID != "intro-a" {
		t.Fatalf("unit[0] = index %d clip %q", units[0].ClipIndex, units[0].Clip.ID)
	}
	if units[1].ClipIndex != 1 || units[1].Clip.ID != "intro-b" {
		t.Fatalf("unit[1] = index %d clip %q, want second fixed clip", units[1].ClipIndex, units[1].Clip.ID)
	}

	generated := Scene{
		ID: "scene-0", Index: 0, ExecutionMode: scriptpkg.SceneExecutionGenerated,
		Clip:  &ClipReference{ID: "clip-a"},
		Clips: []*ClipReference{{ID: "clip-a"}, {ID: "clip-b"}},
	}
	genUnits := RenderUnitsForScene(generated)
	if len(genUnits) != 1 || genUnits[0].Clip.ID != "clip-a" {
		t.Fatalf("generated units = %+v, want single primary-clip unit", genUnits)
	}

	if count := RenderUnitCount([]Scene{fixed, generated}); count != 3 {
		t.Fatalf("RenderUnitCount = %d, want 3 (2 fixed + 1 generated)", count)
	}
}

// TestLocalizedRenderCaptionTextNeverFallsBackForFixedMedia certifies that a
// fixed scene with no display text renders with an empty caption: the BODY
// source_text must never leak into the intro/outro localized render.
func TestLocalizedRenderCaptionTextNeverFallsBackForFixedMedia(t *testing.T) {
	req := GenerateRequest{SourceLanguage: "en", Source: Source{Type: SourceClips, SourceText: "BODY SOURCE TEXT"}}
	fixed := Scene{ID: "scene-intro", ExecutionMode: scriptpkg.SceneExecutionFixedMedia, Text: map[Language]string{"en": ""}}
	if text := localizedRenderCaptionText(req, fixed); text != "" {
		t.Fatalf("fixed caption = %q, want empty (no BODY fallback)", text)
	}
	fixedWithDisplay := Scene{ID: "scene-intro", ExecutionMode: scriptpkg.SceneExecutionFixedMedia, Text: map[Language]string{"en": "Welcome"}}
	if text := localizedRenderCaptionText(req, fixedWithDisplay); text != "Welcome" {
		t.Fatalf("fixed caption = %q, want display text", text)
	}
	generated := Scene{ID: "scene-0", ExecutionMode: scriptpkg.SceneExecutionGenerated, Text: map[Language]string{"en": ""}}
	if text := localizedRenderCaptionText(req, generated); text != "BODY SOURCE TEXT" {
		t.Fatalf("generated caption = %q, want BODY fallback preserved", text)
	}
}

func TestFixedMediaClipProjectionUsesOriginalClipAudioAndSourceWindow(t *testing.T) {
	clips, intents, durationUS := fixedMediaClipProjection(
		[]string{"intro-1", "intro-2"},
		scriptpkg.FixedPlaybackPolicy{AudioMode: scriptpkg.FixedPlaybackOriginalClip, SourceInMS: 1000, SourceOutMS: 4000},
	)
	if len(clips) != 2 || len(intents) != 2 || durationUS != 6_000_000 {
		t.Fatalf("projection = clips:%d intents:%d duration:%d", len(clips), len(intents), durationUS)
	}
	for i, intent := range intents {
		if intent.Mode != capabilityaudio.AudioClip || !intent.UseOriginalAudio || !intent.ProtectedOriginalAudio || intent.ClipAssetID != clips[i].ID {
			t.Fatalf("intent[%d] = %+v, want original CLIP_AUDIO", i, intent)
		}
		if intent.SourceInUS != 1_000_000 || intent.SourceDurationUS != 3_000_000 {
			t.Fatalf("intent[%d] source window = %+v", i, intent)
		}
	}
}

func TestFixedSectionsCompileWithOriginalAudioOnlyAcrossMixPolicies(t *testing.T) {
	req := GenerateRequest{
		SourceLanguage: "en",
		Intro: &scriptpkg.FixedSection{
			ClipIDs:  []string{"intro-clip"},
			Playback: scriptpkg.FixedPlaybackPolicy{AudioMode: scriptpkg.FixedPlaybackOriginalClip, SourceInMS: 0, SourceOutMS: 3_000},
		},
		Outro: &scriptpkg.FixedSection{
			ClipIDs:  []string{"outro-clip"},
			Playback: scriptpkg.FixedPlaybackPolicy{AudioMode: scriptpkg.FixedPlaybackOriginalClip, SourceInMS: 0, SourceOutMS: 2_000},
		},
	}
	scenes, err := applyFixedSections(req, []Scene{{
		ID: "body", Index: 0, DurationUS: 5_000_000,
		AudioIntents: []capabilityaudio.AudioIntent{
			{Mode: capabilityaudio.AudioVoiceover, VoiceoverAssetID: "body-vo", SourceDurationUS: 5_000_000, TimelineDurationUS: 5_000_000},
			{Mode: capabilityaudio.AudioClip, ClipAssetID: "body-clip", SourceDurationUS: 5_000_000, TimelineDurationUS: 5_000_000, UseOriginalAudio: true},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, policy := range []capabilityaudio.AudioMixPolicy{capabilityaudio.MixVoiceoverOnly, capabilityaudio.MixVoiceoverWithDuckedClip} {
		resolved, err := ResolveScenes(scenes, "en", capabilityaudio.AudioModeCombinedTimeline, false)
		if err != nil {
			t.Fatalf("resolve scenes for policy %q: %v", policy, err)
		}
		timeline, err := compileResolvedSceneTimeline(resolved)
		if err != nil {
			t.Fatalf("compile timeline for policy %q: %v", policy, err)
		}
		plan, err := capabilityaudio.CompileWithMixPolicy(timeline, capabilityaudio.DefaultAudioProfile(), policy)
		if err != nil {
			t.Fatalf("compile audio for policy %q: %v", policy, err)
		}
		voiceover := eventsForRole(plan, capabilityaudio.TrackVoiceover)
		for _, event := range voiceover {
			if event.TimelineStartUS < 3_000_000 || event.TimelineStartUS+event.DurationUS > 8_000_000 {
				t.Fatalf("policy %q emitted voiceover inside fixed section: %+v", policy, event)
			}
		}
		clipEvents := eventsForRole(plan, capabilityaudio.TrackClipAudio)
		for _, event := range clipEvents {
			if event.AssetID == "intro-clip" || event.AssetID == "outro-clip" {
				if !event.ProtectedOriginalAudio || !event.UseOriginalAudio || event.GainDB != 0 {
					t.Fatalf("policy %q altered fixed clip audio: %+v", policy, event)
				}
			}
		}
	}
}

func TestResolveScenesFixedMediaDropsGeneratedAudioAndProtectsOriginalClip(t *testing.T) {
	scenes, err := ResolveScenes([]Scene{{
		ID: "scene-intro", Index: 0, DurationUS: 3_000_000,
		ExecutionMode: scriptpkg.SceneExecutionFixedMedia,
		AudioIntents: []capabilityaudio.AudioIntent{
			{Mode: capabilityaudio.AudioVoiceover, VoiceoverAssetID: "must-not-render", SourceDurationUS: 3_000_000, TimelineDurationUS: 3_000_000},
			{Mode: capabilityaudio.AudioClip, ClipAssetID: "intro-clip", SourceDurationUS: 3_000_000, TimelineDurationUS: 3_000_000},
		},
		Clips: []*ClipReference{{ID: "intro-clip", SourceInMS: 0, SourceOutMS: 3000}},
	}}, "en", capabilityaudio.AudioModeCombinedTimeline, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(scenes) != 1 || len(scenes[0].AudioIntents) != 1 {
		t.Fatalf("resolved fixed scene intents = %+v, want one clip intent", scenes)
	}
	intent := scenes[0].AudioIntents[0]
	if intent.Mode != capabilityaudio.AudioClip || intent.ClipAssetID != "intro-clip" || !intent.UseOriginalAudio || !intent.ProtectedOriginalAudio || intent.GainDB != 0 {
		t.Fatalf("resolved fixed audio = %+v, want protected original clip audio", intent)
	}
}
