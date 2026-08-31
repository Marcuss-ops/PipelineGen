package scriptgeneration

import (
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

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
