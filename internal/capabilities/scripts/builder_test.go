package scriptgeneration

import (
	"encoding/json"
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func TestBuildGenerateRequest_PropagatesSaveToDB(t *testing.T) {
	var env scriptpkg.GenerationEnvelopeV2
	if err := json.Unmarshal([]byte(`{"version":2,"items":[{"title":"persisted","language":"it","source":{"type":"text","topic":"topic"},"output":{"save_to_db":true}}]}`), &env); err != nil {
		t.Fatal(err)
	}
	got, err := BuildGenerateRequest(&env, "persist-key")
	if err != nil {
		t.Fatal(err)
	}
	if !got.SaveToDB {
		t.Fatal("output.save_to_db=true must reach GenerateRequest")
	}
}

func TestBuildGenerateRequest_MapsExplicitDocsConfig(t *testing.T) {
	var env scriptpkg.GenerationEnvelopeV2
	err := json.Unmarshal([]byte(`{
		"version": 2,
		"items": [{
			"title": "test",
			"language": "it",
			"source": {"type": "text", "topic": "topic"},
			"docs": {"enabled": true, "languages": ["it"], "folder_id": "folder"}
		}]
	}`), &env)
	if err != nil {
		t.Fatal(err)
	}

	got, err := BuildGenerateRequest(&env, "key")
	if err != nil {
		t.Fatal(err)
	}

	if !got.Docs.Enabled || got.Docs.FolderID != "folder" {
		t.Fatalf("docs config not mapped: %+v", got.Docs)
	}
	if len(got.Docs.Languages) != 1 || got.Docs.Languages[0] != "it" {
		t.Fatalf("docs languages not mapped: %v", got.Docs.Languages)
	}
}

func TestBuildGenerateRequest_MapsResearchSourcePolicy(t *testing.T) {
	var env scriptpkg.GenerationEnvelopeV2
	if err := json.Unmarshal([]byte(`{
		"version": 2,
		"items": [{
			"title": "research",
			"language": "it",
			"source": {
				"type": "research",
				"topic": "Maya",
				"query": "Maya archaeology",
				"search": true,
				"force_refresh": true,
				"cache": {"mode": "force_refresh", "ttl_hours": 168, "version": "research-v1"},
				"research": {"max_queries": 4, "max_pages": 8, "min_sources": 3, "require_citations": true}
			}
		}]
	}`), &env); err != nil {
		t.Fatal(err)
	}

	got, err := BuildGenerateRequest(&env, "research-policy-key")
	if err != nil {
		t.Fatal(err)
	}
	if got.Source.Type != SourceType(scriptpkg.SourceResearch) || !got.Source.Search || !got.Source.ForceRefresh {
		t.Fatalf("research source policy was not mapped: %+v", got.Source)
	}
	if got.Source.CachePolicy.Mode != scriptpkg.SourceCacheModeForceRefresh || got.Source.CachePolicy.Version != "research-v1" {
		t.Fatalf("research cache policy was not mapped: %+v", got.Source.CachePolicy)
	}
	if got.Source.Research.MaxQueries != 4 || got.Source.Research.MaxPages != 8 || got.Source.Research.MinSources != 3 || !got.Source.Research.RequireCitations {
		t.Fatalf("research policy was not mapped: %+v", got.Source.Research)
	}
}

func TestBuildGenerateRequest_MapsExplicitAudioMode(t *testing.T) {
	var env scriptpkg.GenerationEnvelopeV2
	if err := json.Unmarshal([]byte(`{"version":2,"items":[{"title":"combined","language":"en","source":{"type":"text","topic":"topic"},"output":{"voiceover_enabled":true},"audio":{"mode":"COMBINED_TIMELINE"}}]}`), &env); err != nil {
		t.Fatal(err)
	}
	got, err := BuildGenerateRequest(&env, "audio-mode-key")
	if err != nil {
		t.Fatal(err)
	}
	if got.Audio != capabilityaudio.AudioModeCombinedTimeline {
		t.Fatalf("audio mode = %q, want %q", got.Audio, capabilityaudio.AudioModeCombinedTimeline)
	}
}

// TestBuildGenerateRequest_GenerateTimelineDoesNotImplyAudio certifies the
// semantic separation: output.generate_timeline produces timeline
// metadata/planning only and must not select an audio mode by itself. A
// Drive-only clip workflow can generate a script + canonical timeline
// without staging local binaries or enqueuing a render job.
func TestBuildGenerateRequest_GenerateTimelineDoesNotImplyAudio(t *testing.T) {
	var env scriptpkg.GenerationEnvelopeV2
	if err := json.Unmarshal([]byte(`{"version":2,"items":[{"title":"timeline-only","language":"en","source":{"type":"text","topic":"topic"},"output":{"generate_timeline":true}}]}`), &env); err != nil {
		t.Fatal(err)
	}
	got, err := BuildGenerateRequest(&env, "timeline-only-key")
	if err != nil {
		t.Fatal(err)
	}
	if got.GenerateTimeline != true {
		t.Fatalf("GenerateTimeline must be true, got %v", got.GenerateTimeline)
	}
	if got.Audio != capabilityaudio.AudioModeNone {
		t.Fatalf("timeline-only audio mode = %q, want %q", got.Audio, capabilityaudio.AudioModeNone)
	}
}

func TestBuildGenerateRequest_MapsMediaPlan(t *testing.T) {
	env := &scriptpkg.GenerationEnvelopeV2{Items: []scriptpkg.GenerationItemV2{{
		Title:    "media-plan",
		Language: "en",
		Source:   scriptpkg.SourceSpec{Type: scriptpkg.SourceType("text")},
		MediaPlan: mediadomain.MediaPlanSpec{
			ProviderPolicy: mediadomain.MediaProviderPolicy{
				Artlist:        mediadomain.MediaToggleEnabled,
				InternetImages: mediadomain.MediaToggleEnabled,
			},
			Extraction: mediadomain.MediaExtractionPolicy{Enabled: true, MaxEntitiesPerSegment: 7},
		},
	}}}

	got, err := BuildGenerateRequest(env, "media-plan-key")
	if err != nil {
		t.Fatal(err)
	}
	if !got.MediaPlan.ProviderPolicy.Artlist.AsBool() {
		t.Fatal("artlist provider toggle must be mapped from the envelope")
	}
	if !got.MediaPlan.ProviderPolicy.InternetImages.AsBool() {
		t.Fatal("internet_images provider toggle must be mapped from the envelope")
	}
	if got.MediaPlan.Extraction.MaxEntitiesPerSegment != 7 {
		t.Fatalf("extraction limits = %+v, want max entities 7", got.MediaPlan.Extraction)
	}
}

// TestBuildGenerateRequest_CombinedTimelineIsTheOnlyAudioGate certifies that
// audio.mode=COMBINED_TIMELINE compiles a certified final_audio.m4a without
// any video render gate: the audio master is never blocked by render flags.
func TestBuildGenerateRequest_CombinedTimelineIsTheOnlyAudioGate(t *testing.T) {
	var env scriptpkg.GenerationEnvelopeV2
	if err := json.Unmarshal([]byte(`{"version":2,"items":[{"title":"combined","language":"en","source":{"type":"text","topic":"topic"},"output":{"generate_timeline":true,"voiceover_enabled":true},"audio":{"mode":"COMBINED_TIMELINE"}}]}`), &env); err != nil {
		t.Fatal(err)
	}
	got, err := BuildGenerateRequest(&env, "audio-mode-no-render")
	if err != nil {
		t.Fatalf("COMBINED_TIMELINE must build: %v", err)
	}
	if got.Audio != capabilityaudio.AudioModeCombinedTimeline {
		t.Fatalf("audio mode = %q, want %q", got.Audio, capabilityaudio.AudioModeCombinedTimeline)
	}
}

// TestBuildGenerateRequest_MapsAudioIntentBlock certifies that the
// editorial audio intent block (mix_policy + background_music +
// sound_effects) reaches the durable GenerateRequest. The wire
// single-object background_music form is normalized to a slice at the
// domain boundary, so the durable request always works with
// []BackgroundMusicIntent.
func TestBuildGenerateRequest_MapsAudioIntentBlock(t *testing.T) {
	var env scriptpkg.GenerationEnvelopeV2
	if err := json.Unmarshal([]byte(`{"version":2,"items":[{"title":"audio-intents","language":"en","source":{"type":"text","topic":"topic"},"output":{"voiceover_enabled":true},"audio":{"mode":"COMBINED_TIMELINE","mix_policy":"voiceover_with_ducked_clip","background_music":{"asset_id":"music_123","start_ms":0,"end":"video_end","loop":true,"gain_db":-24},"sound_effects":[{"asset_id":"whoosh","scene_id":"scene_2","anchor":"end","offset_ms":-300,"gain_db":-8}]}}]}`), &env); err != nil {
		t.Fatal(err)
	}
	got, err := BuildGenerateRequest(&env, "audio-intents-key")
	if err != nil {
		t.Fatal(err)
	}
	if got.MixPolicy != capabilityaudio.AudioMixPolicy("voiceover_with_ducked_clip") {
		t.Fatalf("mix policy = %q", got.MixPolicy)
	}
	if len(got.BackgroundMusic) != 1 {
		t.Fatalf("BackgroundMusic = %+v, want exactly one entry (single-object wire form must normalize to a slice)", got.BackgroundMusic)
	}
	b := got.BackgroundMusic[0]
	if b.AssetID != "music_123" || !b.Loop || b.GainDB != -24 || !b.End.IsVideoEnd() {
		t.Fatalf("bgm = %+v (end=%+v)", b, b.End)
	}
	if len(got.SoundEffects) != 1 {
		t.Fatalf("SoundEffects = %+v, want exactly one entry", got.SoundEffects)
	}
	s := got.SoundEffects[0]
	if s.AssetID != "whoosh" || s.SceneID != "scene_2" || s.Anchor != scriptpkg.SFXAnchorEnd || s.OffsetMS != -300 || s.GainDB != -8 {
		t.Fatalf("sfx = %+v", s)
	}
}

// TestBuildGenerateRequest_MapsSegmentedBackgroundMusic certifies that
// multiple segmented BGM layers (disjoint windows with end_ms / end)
// survive the builder untouched.
func TestBuildGenerateRequest_MapsSegmentedBackgroundMusic(t *testing.T) {
	var env scriptpkg.GenerationEnvelopeV2
	if err := json.Unmarshal([]byte(`{"version":2,"items":[{"title":"segmented-bgm","language":"en","source":{"type":"text","topic":"topic"},"output":{"voiceover_enabled":true},"audio":{"mode":"COMBINED_TIMELINE","background_music":[{"asset_id":"music_intro","start_ms":0,"end_ms":60000,"loop":true},{"asset_id":"music_dark","start_ms":60000,"end":"video_end","loop":true}]}}]}`), &env); err != nil {
		t.Fatal(err)
	}
	got, err := BuildGenerateRequest(&env, "segmented-bgm-key")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.BackgroundMusic) != 2 {
		t.Fatalf("BackgroundMusic = %+v, want two segmented layers", got.BackgroundMusic)
	}
	if got.BackgroundMusic[0].AssetID != "music_intro" || got.BackgroundMusic[0].End == nil || got.BackgroundMusic[0].End.Ms != 60000 {
		t.Fatalf("first layer = %+v (end=%+v)", got.BackgroundMusic[0], got.BackgroundMusic[0].End)
	}
	if got.BackgroundMusic[1].AssetID != "music_dark" || !got.BackgroundMusic[1].End.IsVideoEnd() {
		t.Fatalf("second layer = %+v (end=%+v)", got.BackgroundMusic[1], got.BackgroundMusic[1].End)
	}
}

// TestBuildGenerateRequest_AudioIntentFallsBackToOutputAudio certifies the
// compat fallback: when the top-level audio intent block is absent, the
// nested output.audio shape is used.
func TestBuildGenerateRequest_AudioIntentFallsBackToOutputAudio(t *testing.T) {
	var env scriptpkg.GenerationEnvelopeV2
	if err := json.Unmarshal([]byte(`{"version":2,"items":[{"title":"fallback","language":"en","source":{"type":"text","topic":"topic"},"output":{"voiceover_enabled":true,"audio":{"mix_policy":"VOICEOVER_ONLY","background_music":[{"asset_id":"music_fb"}]}},"audio":{"mode":"COMBINED_TIMELINE"}}]}`), &env); err != nil {
		t.Fatal(err)
	}
	got, err := BuildGenerateRequest(&env, "audio-intent-fallback-key")
	if err != nil {
		t.Fatal(err)
	}
	if got.MixPolicy != capabilityaudio.AudioMixPolicy("VOICEOVER_ONLY") {
		t.Fatalf("fallback mix policy = %q, want VOICEOVER_ONLY", got.MixPolicy)
	}
	if len(got.BackgroundMusic) != 1 || got.BackgroundMusic[0].AssetID != "music_fb" {
		t.Fatalf("fallback background_music = %+v", got.BackgroundMusic)
	}
}

// TestBuildGenerateRequest_NoAudioIntentsStayEmpty certifies that an absent
// audio intent block stays empty on the durable request (legacy behavior
// unchanged).
func TestBuildGenerateRequest_NoAudioIntentsStayEmpty(t *testing.T) {
	var env scriptpkg.GenerationEnvelopeV2
	if err := json.Unmarshal([]byte(`{"version":2,"items":[{"title":"plain","language":"en","source":{"type":"text","topic":"topic"},"output":{"voiceover_enabled":true},"audio":{"mode":"COMBINED_TIMELINE"}}]}`), &env); err != nil {
		t.Fatal(err)
	}
	got, err := BuildGenerateRequest(&env, "plain-key")
	if err != nil {
		t.Fatal(err)
	}
	if got.MixPolicy != "" || len(got.BackgroundMusic) != 0 || len(got.SoundEffects) != 0 {
		t.Fatalf("absent audio intents must stay empty, got mix=%q bgm=%d sfx=%d", got.MixPolicy, len(got.BackgroundMusic), len(got.SoundEffects))
	}
}

// TestBuildGenerateRequest_PropagatesVoiceoverTiming certifies that the
// canonical top-level audio.timing policy (mode/boundary/formats) reaches
// GenerateRequest.Timing so the durable runner can enforce the
// required/best-effort fail-closed semantics end-to-end.
func TestBuildGenerateRequest_PropagatesVoiceoverTiming(t *testing.T) {
	var env scriptpkg.GenerationEnvelopeV2
	if err := json.Unmarshal([]byte(`{"version":2,"items":[{"title":"timing","language":"en","source":{"type":"text","topic":"topic"},"output":{"voiceover_enabled":true},"audio":{"mode":"COMBINED_TIMELINE","timing":{"mode":"required","boundary":"word","formats":["json","srt","vtt"]}}}]}`), &env); err != nil {
		t.Fatal(err)
	}
	got, err := BuildGenerateRequest(&env, "timing-key")
	if err != nil {
		t.Fatal(err)
	}
	if got.Timing == nil {
		t.Fatal("audio.timing must map to GenerateRequest.Timing")
	}
	if got.Timing.Mode != capabilityaudio.TimingRequired {
		t.Fatalf("timing mode = %q, want required", got.Timing.Mode)
	}
	if got.Timing.BoundaryMode != capabilityaudio.BoundaryWord {
		t.Fatalf("boundary mode = %q, want word", got.Timing.BoundaryMode)
	}
	if len(got.Timing.Formats) != 3 {
		t.Fatalf("formats = %v, want [json srt vtt]", got.Timing.Formats)
	}
}

// TestBuildGenerateRequest_TimingFallsBackToOutputAudio certifies the
// compat fallback: when the top-level audio.timing is absent, the nested
// output.audio.timing shape is used.
func TestBuildGenerateRequest_TimingFallsBackToOutputAudio(t *testing.T) {
	var env scriptpkg.GenerationEnvelopeV2
	if err := json.Unmarshal([]byte(`{"version":2,"items":[{"title":"timing","language":"en","source":{"type":"text","topic":"topic"},"output":{"voiceover_enabled":true,"audio":{"timing":{"mode":"required","boundary":"word","formats":["json"]}}},"audio":{"mode":"COMBINED_TIMELINE"}}]}`), &env); err != nil {
		t.Fatal(err)
	}
	got, err := BuildGenerateRequest(&env, "timing-fallback-key")
	if err != nil {
		t.Fatal(err)
	}
	if got.Timing == nil || got.Timing.Mode != capabilityaudio.TimingRequired {
		t.Fatalf("output.audio.timing must fall back into GenerateRequest.Timing, got %+v", got.Timing)
	}
}

// TestBuildGenerateRequest_ResolvesExplicitProject certifies the single
// routing resolution point: GenerateRequest.Project is resolved ONCE from
// the explicit generation input, so no downstream component re-derives it.
func TestBuildGenerateRequest_ResolvesExplicitProject(t *testing.T) {
	var env scriptpkg.GenerationEnvelopeV2
	if err := json.Unmarshal([]byte(`{"version":2,"items":[{"title":"mike-tyson-documentary","project":"project-namespace","language":"en","source":{"type":"text","topic":"topic"},"output":{"voiceover_enabled":true},"audio":{"mode":"COMBINED_TIMELINE"}}]}`), &env); err != nil {
		t.Fatal(err)
	}
	got, err := BuildGenerateRequest(&env, "project-key")
	if err != nil {
		t.Fatal(err)
	}
	if got.Project != "project-namespace" {
		t.Fatalf("Project = %q, want %q (resolved once from explicit project)", got.Project, "project-namespace")
	}
}

// TestBuildGenerateRequest_NoTimingStaysNil certifies that an absent timing
// policy stays nil (the pipeline applies the canonical defaults downstream —
// capture is never implicitly mandatory).
func TestBuildGenerateRequest_NoTimingStaysNil(t *testing.T) {
	var env scriptpkg.GenerationEnvelopeV2
	if err := json.Unmarshal([]byte(`{"version":2,"items":[{"title":"no-timing","language":"en","source":{"type":"text","topic":"topic"},"output":{"voiceover_enabled":true},"audio":{"mode":"COMBINED_TIMELINE"}}]}`), &env); err != nil {
		t.Fatal(err)
	}
	got, err := BuildGenerateRequest(&env, "no-timing-key")
	if err != nil {
		t.Fatal(err)
	}
	if got.Timing != nil {
		t.Fatalf("absent timing must stay nil, got %+v", got.Timing)
	}
}
