package adapters

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func TestMergePostProcessResult_PreservesReorderedMultiClipBindings(t *testing.T) {
	currentInput := &ProcessInput{SpecScene: scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{
			{
				ID: "scene-a", SegmentID: "segment-a", Index: 0, Text: "old a",
				Bindings: scriptpkg.SceneBindings{Clips: []scriptpkg.ClipBinding{
					{ClipID: "clip-a", ClipTitle: "A", DriveLink: "https://drive/a", SubtitleLink: "https://drive/sub-a", SubtitleFileID: "sub-a", StartMs: 100, EndMs: 1100, DurationMs: 1000},
					{ClipID: "clip-b", ClipTitle: "B", DriveLink: "https://drive/b", SubtitleLink: "https://drive/sub-b", SubtitleFileID: "sub-b", StartMs: 200, EndMs: 2200, DurationMs: 2000},
				}},
			},
			{ID: "scene-b", SegmentID: "segment-b", Index: 1, Text: "old b"},
		},
	}}

	// The replacement is reordered by segment identity and contains only a
	// partial clip entry. Neither operation may discard the other clip or its
	// render-critical metadata.
	src := &PostProcessResult{UpdatedSpecScene: scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{
			{ID: "scene-b", SegmentID: "segment-b", Index: 0, Text: "new b"},
			{ID: "scene-a", SegmentID: "segment-a", Index: 1, Text: "new a", Bindings: scriptpkg.SceneBindings{Clips: []scriptpkg.ClipBinding{{ClipID: "clip-b"}}}},
		},
	}}

	mergePostProcessResult(&PipelineResult{}, src, currentInput)

	if got := currentInput.SpecScene.Scenes[0].Bindings.Clips; len(got) != 0 {
		t.Fatalf("reordered scene-b unexpectedly inherited scene-a clips: %#v", got)
	}
	clips := currentInput.SpecScene.Scenes[1].Bindings.Clips
	if len(clips) != 2 {
		t.Fatalf("scene-a clips = %d, want 2 after partial replacement: %#v", len(clips), clips)
	}
	if clips[0].ClipID != "clip-b" || clips[1].ClipID != "clip-a" {
		t.Fatalf("scene-a clip order = [%s %s], want [clip-b clip-a]", clips[0].ClipID, clips[1].ClipID)
	}
	if clips[0].DriveLink != "https://drive/b" || clips[0].SubtitleLink != "https://drive/sub-b" || clips[0].SubtitleFileID != "sub-b" || clips[0].StartMs != 200 || clips[0].EndMs != 2200 || clips[0].DurationMs != 2000 {
		t.Fatalf("clip-b metadata was not preserved: %#v", clips[0])
	}
	if currentInput.SpecScene.Scenes[1].Bindings.Clip == nil || currentInput.SpecScene.Scenes[1].Bindings.Clip.ClipID != "clip-b" {
		t.Fatalf("legacy clip alias does not point to the first canonical clip: %#v", currentInput.SpecScene.Scenes[1].Bindings)
	}
}

func TestMergePostProcessResult_PreservesVoiceoverFieldsAndLanguageLinks(t *testing.T) {
	currentInput := &ProcessInput{SpecScene: scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{{
			ID: "scene-1", SegmentID: "segment-1", Index: 0, Text: "source",
		}},
	}}
	result := &PipelineResult{}

	mergePostProcessResult(result, &PostProcessResult{Voiceovers: []SceneVoiceover{
		{SceneIndex: 0, Language: "en", Status: "completed", Link: "https://drive/vo-en", LocalPath: "/tmp/vo-en.mp3", DurationMs: 4100},
		{SceneIndex: 0, Language: "it", Status: "completed", Link: "https://drive/vo-it", LocalPath: "/tmp/vo-it.mp3", DurationMs: 4200},
	}}, currentInput)

	binding := currentInput.SpecScene.Scenes[0].Bindings.Voiceover
	if binding == nil {
		t.Fatal("voiceover binding was not materialized")
	}
	if binding.Status != "completed" || binding.Link != "https://drive/vo-en" || binding.LocalPath != "/tmp/vo-en.mp3" || binding.DurationMs != 4100 {
		t.Fatalf("default voiceover fields = %#v, want first completed language fields", binding)
	}
	if binding.Links["en"] != "https://drive/vo-en" || binding.Links["it"] != "https://drive/vo-it" {
		t.Fatalf("language voiceover links = %#v, want both languages", binding.Links)
	}

	// A later translated/updated scene with an empty binding must retain the
	// complete voiceover binding rather than replacing it with zero values.
	mergePostProcessResult(&PipelineResult{}, &PostProcessResult{UpdatedSpecScene: scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes:  []scriptpkg.SpecScene{{ID: "scene-1", SegmentID: "segment-1", Index: 0, Text: "translated"}},
	}}, currentInput)
	binding = currentInput.SpecScene.Scenes[0].Bindings.Voiceover
	if binding == nil || binding.Link != "https://drive/vo-en" || binding.LocalPath != "/tmp/vo-en.mp3" || binding.DurationMs != 4100 || binding.Links["it"] != "https://drive/vo-it" {
		t.Fatalf("voiceover fields were lost after scene replacement: %#v", binding)
	}
}

// TestMergePostProcessResult_PreservesVoiceoverTimingMap pins the
// per-language timing write-back + preservation contract: the merge
// writes VoiceoverBinding.Timing[language] from each SceneVoiceover,
// and both the scene-replacement (translation/synthesis) and the
// translated-scene write-back paths retain previously produced timing
// links — timing links are never erased.
func TestMergePostProcessResult_PreservesVoiceoverTimingMap(t *testing.T) {
	currentInput := &ProcessInput{SpecScene: scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes:  []scriptpkg.SpecScene{{ID: "scene-1", SegmentID: "segment-1", Index: 0, Text: "source"}},
	}}

	// First merge: two languages, one completed bundle + one explicit
	// best-effort failure (failed status must stay visible).
	mergePostProcessResult(&PipelineResult{}, &PostProcessResult{Voiceovers: []SceneVoiceover{
		{SceneIndex: 0, Language: "en", Status: "completed", Link: "https://drive/vo-en", Timing: &scriptpkg.VoiceoverTimingBinding{
			Status: "completed", JSONLink: "https://drive/timing-en.json", SRTLink: "https://drive/timing-en.srt",
			BoundaryMode: "word", WordCount: 184, DurationUS: 18_342_000, TextSHA256: "text-en", AudioSHA256: "audio-en",
		}},
		{SceneIndex: 0, Language: "it", Status: "completed", Link: "https://drive/vo-it", Timing: &scriptpkg.VoiceoverTimingBinding{
			Status: "failed",
		}},
	}}, currentInput)

	binding := currentInput.SpecScene.Scenes[0].Bindings.Voiceover
	if binding == nil {
		t.Fatal("voiceover binding was not materialized")
	}
	if binding.Timing == nil || binding.Timing["en"].JSONLink != "https://drive/timing-en.json" || binding.Timing["en"].WordCount != 184 || binding.Timing["en"].AudioSHA256 != "audio-en" {
		t.Fatalf("en timing bundle was not written: %#v", binding.Timing)
	}
	if binding.Timing["it"].Status != "failed" {
		t.Fatalf("best-effort timing failure must stay visible per language: %#v", binding.Timing["it"])
	}

	// Phase 2 — scene replacement (translation/synthesis path): an empty
	// replacement scene must retain the complete timing map.
	mergePostProcessResult(&PipelineResult{}, &PostProcessResult{UpdatedSpecScene: scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes:  []scriptpkg.SpecScene{{ID: "scene-1", SegmentID: "segment-1", Index: 0, Text: "translated"}},
	}}, currentInput)
	binding = currentInput.SpecScene.Scenes[0].Bindings.Voiceover
	if binding == nil || binding.Timing["en"].JSONLink != "https://drive/timing-en.json" || binding.Timing["it"].Status != "failed" {
		t.Fatalf("timing links were erased after scene replacement: %#v", binding)
	}

	// Phase 3 — translated-scene write-back: same preservation guarantee
	// through the TranslatedSpecScene path.
	mergePostProcessResult(&PipelineResult{}, &PostProcessResult{TranslatedSpecScene: scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes:  []scriptpkg.SpecScene{{ID: "scene-1", SegmentID: "segment-1", Index: 0, Text: "tradotto"}},
	}}, currentInput)
	binding = currentInput.SpecScene.Scenes[0].Bindings.Voiceover
	if binding == nil || binding.Timing["en"].JSONLink != "https://drive/timing-en.json" || binding.Timing["it"].Status != "failed" {
		t.Fatalf("timing links were erased by translation write-back: %#v", binding)
	}
}
