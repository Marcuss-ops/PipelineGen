package scriptgeneration

import (
	"context"
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestApplySegmentNoVoiceoverScenesKeepsDatabaseClipAndSkipsTTS(t *testing.T) {
	req := GenerateRequest{
		SourceLanguage: "it",
		ScriptParams: scriptpkg.ScriptSpec{Segments: []scriptpkg.ScriptSegment{{
			ID:          "cut-01",
			NoVoiceover: true,
			DurationMS:  8000,
			ClipIDs:     []string{"cliprender_cut"},
		}}},
	}
	clip := &ClipReference{ID: "cliprender_cut", Path: "/tmp/cliprender_cut.mp4"}
	scenes := []Scene{{
		ID: "cut-01", Index: 0, Clip: clip,
		Text: map[Language]string{"it": "descrizione non parlata"},
	}}

	applySegmentNoVoiceoverScenes(req, scenes)

	if !scenes[0].ExecutionMode.IsFixedMedia() {
		t.Fatalf("execution mode = %q, want fixed_media", scenes[0].ExecutionMode)
	}
	if !scenes[0].NativeClip {
		t.Fatal("the cutaway must be marked native_clip")
	}
	if scenes[0].FixedPlayback == nil || !scenes[0].FixedPlayback.Valid() {
		t.Fatalf("fixed playback = %+v, want a valid original_clip policy", scenes[0].FixedPlayback)
	}
	if scenes[0].FixedPlayback.AudioMode != scriptpkg.FixedPlaybackOriginalClip || scenes[0].FixedPlayback.SourceInMS != 0 || scenes[0].FixedPlayback.SourceOutMS != 8000 {
		t.Fatalf("fixed playback = %+v, want original_clip window 0..8000ms", scenes[0].FixedPlayback)
	}
	if scenes[0].ExecutionMode.AllowsTTS() {
		t.Fatal("the cutaway must not allow TTS")
	}
	if scenes[0].DurationMS != 8000 || scenes[0].DurationUS != 8_000_000 {
		t.Fatalf("duration = %dms/%dus, want 8000ms/8000000us", scenes[0].DurationMS, scenes[0].DurationUS)
	}
	if scenes[0].Text["it"] != "" {
		t.Fatalf("cutaway text = %q, want empty (the clip is the text surface)", scenes[0].Text["it"])
	}
	if clip.SourceInMS != 0 || clip.SourceOutMS != 8000 || clip.DurationUS != 8_000_000 {
		t.Fatalf("clip window = in:%d out:%d duration:%d, want 0/8000/8000000", clip.SourceInMS, clip.SourceOutMS, clip.DurationUS)
	}
	if len(scenes[0].AudioIntents) != 1 {
		t.Fatalf("audio intents = %+v, want exactly one protected original-clip intent", scenes[0].AudioIntents)
	}
	intent := scenes[0].AudioIntents[0]
	if intent.Mode != capabilityaudio.AudioClip || intent.ClipAssetID != "cliprender_cut" || !intent.UseOriginalAudio || !intent.ProtectedOriginalAudio {
		t.Fatalf("audio intent = %+v, want protected original clip audio", intent)
	}
}

func TestValidateClipSceneOutputSkipsNativeCutaway(t *testing.T) {
	req := GenerateRequest{SourceLanguage: "it"}
	scenes := []Scene{{
		ID: "cut-01", Index: 1,
		ExecutionMode: scriptpkg.SceneExecutionFixedMedia, NativeClip: true,
		Text: map[Language]string{"it": ""},
	}}
	if !(&Runner{}).validateClipSceneOutput(context.Background(), "run", req, ExecutionContext{}, ExecutionStep{}, scenes) {
		t.Fatal("a native cutaway must not be validated as narration")
	}
}

func TestFixedMediaNeedsLocalizedRenderSkipsNativeClipCutaways(t *testing.T) {
	req := GenerateRequest{
		SourceLanguage: "it",
		Render:         scriptpkg.VideoRenderSpec{Subtitles: &scriptpkg.VideoSubtitlesSpec{Enabled: true}},
	}
	opening := Scene{ExecutionMode: scriptpkg.SceneExecutionFixedMedia, Role: scriptpkg.SceneRoleOpening, Text: map[Language]string{"it": "Apertura"}}
	closing := Scene{ExecutionMode: scriptpkg.SceneExecutionFixedMedia, Role: scriptpkg.SceneRoleClosing, Text: map[Language]string{"it": "Chiusura"}}
	body := Scene{ExecutionMode: scriptpkg.SceneExecutionFixedMedia, NativeClip: true, Role: scriptpkg.SceneRoleBody, Text: map[Language]string{"it": ""}}
	if !fixedMediaNeedsLocalizedRender(req, opening) {
		t.Fatal("a captioned opening must use the localized subtitle render lane")
	}
	if !fixedMediaNeedsLocalizedRender(req, closing) {
		t.Fatal("a captioned closing must use the localized subtitle render lane")
	}
	if fixedMediaNeedsLocalizedRender(req, body) {
		t.Fatal("a body fixed-media clip cutaway must stay native, never localized")
	}
}

func TestBuildFinalJobPayloadsBodyFixedClipUsesDatabaseAsset(t *testing.T) {
	result := &GenerateResult{

		Scenes: []Scene{{
			ID: "cut-01", Index: 0,
			ExecutionMode: scriptpkg.SceneExecutionFixedMedia, NativeClip: true, Role: scriptpkg.SceneRoleBody,
			Clip: &ClipReference{ID: "cliprender_cut", DurationUS: 2_000_000, SourceOutMS: 2_000},
			Text: map[Language]string{"it": ""},
		}}, AudioOutput: AudioOutput{CanonicalTimeline: &capabilityaudio.CanonicalTimeline{
			DurationUS: 2_000_000,
			Segments: []capabilityaudio.TimelineSegment{{
				ID: "cut-01", DurationUS: 2_000_000, FixedMedia: true,
				AudioIntents: []capabilityaudio.AudioIntent{{
					Mode: capabilityaudio.AudioClip, ClipAssetID: "cliprender_cut",
					SourceDurationUS: 2_000_000, TimelineDurationUS: 2_000_000, UseOriginalAudio: true,
				}},
			}},
		},
			FinalAudio: certifiedFinalAudio(2_000)},
	}

	pre, _, err := BuildFinalJobPayloads(context.Background(), "run-cut", GenerateRequest{Title: "Cutaway", SourceLanguage: "it"}, result, finalJobPayloadResolver{})
	if err != nil {
		t.Fatalf("BuildFinalJobPayloads: %v", err)
	}
	plan := decodeFinalJobPlanForTest(t, pre)
	if len(plan.VideoTracks) != 1 || len(plan.VideoTracks[0].Segments) != 1 {
		t.Fatalf("compiled plan = %+v, want one body clip segment", plan.VideoTracks)
	}
	if got := plan.VideoTracks[0].Segments[0].AssetID; got != "drive-asset" {
		t.Fatalf("compiled clip asset = %q, want the database asset resolved by the library (not a localized render)", got)
	}
}
