package adapters_test

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func TestClipBindings_ExplicitSegmentsPreserveZeroToManyOwnership(t *testing.T) {
	evidence := &scriptpkg.ClipEvidence{
		AcceptedClipIDs: []string{"intro-a", "intro-b", "body-a", "body-b"},
		DriveLinks: map[string]string{
			"intro-a": "https://drive/intro-a",
			"intro-b": "https://drive/intro-b",
			"body-a":  "https://drive/body-a",
			"body-b":  "https://drive/body-b",
		},
		ClipDetails: map[string]scriptpkg.ClipDetail{
			"intro-a": {Name: "Intro A", StartMs: 10, EndMs: 1010, SubtitleLink: "https://drive/sub-intro-a", SubtitleFileID: "sub-intro-a"},
			"intro-b": {Name: "Intro B", StartMs: 20, EndMs: 2020},
			"body-a":  {Name: "Body A", StartMs: 30, EndMs: 3030},
			"body-b":  {Name: "Body B", StartMs: 40, EndMs: 4040},
		},
	}
	plan := &scriptpkg.ResolvedGenerationPlan{
		MediaMode:    scriptpkg.MediaModeClipOnly,
		ClipEvidence: evidence,
		Segments: []scriptpkg.ScriptSegment{
			{ID: scriptpkg.IntroHookSegmentID, Kind: "intro", Topic: "Opening", ClipIDs: []string{"intro-a", "intro-b"}},
			{ID: "narration", Kind: "narration", Topic: "Narration", ClipIDs: []string{}},
			{ID: "body", Kind: "scene", Topic: "Body", ClipIDs: []string{"body-a", "body-b"}},
		},
	}
	input := adapters.ProcessInput{SpecScene: scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{
			{ID: "model-0", Index: 0, Text: "Opening", Kind: scriptpkg.SceneIntro},
			{ID: "model-1", Index: 1, Text: "Narration", Kind: scriptpkg.SceneNarration},
			{ID: "model-2", Index: 2, Text: "Body", Kind: scriptpkg.SceneClip},
		},
	}}

	result, err := adapters.NewClipBindingsProcessor(zap.NewNop()).Process(context.Background(), plan, input)
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if result == nil || len(result.UpdatedSpecScene.Scenes) != 3 {
		t.Fatalf("updated scenes = %#v, want exactly 3", result)
	}

	scenes := result.UpdatedSpecScene.Scenes
	if got := scenes[0].SegmentID; got != scriptpkg.IntroHookSegmentID {
		t.Fatalf("intro SegmentID = %q, want %q", got, scriptpkg.IntroHookSegmentID)
	}
	assertBindingIDs(t, scenes[0].Bindings.Clips, []string{"intro-a", "intro-b"})
	assertBindingIDs(t, scenes[1].Bindings.Clips, nil)
	assertBindingIDs(t, scenes[2].Bindings.Clips, []string{"body-a", "body-b"})

	for _, binding := range append(append(scenes[0].Bindings.Clips, scenes[2].Bindings.Clips...), scriptpkg.ClipBinding{}) {
		if binding.ClipID == "" {
			continue
		}
		if binding.DriveLink == "" || binding.EndMs <= binding.StartMs || binding.DurationMs != binding.EndMs-binding.StartMs {
			t.Errorf("incomplete binding: %+v", binding)
		}
	}
	if got := scenes[0].Bindings.Clips[0].SubtitleFileID; got != "sub-intro-a" {
		t.Errorf("subtitle file ID = %q, want sub-intro-a", got)
	}
}

func TestClipBindings_ExplicitReuseAcrossScenesIsPreserved(t *testing.T) {
	evidence := &scriptpkg.ClipEvidence{
		AcceptedClipIDs: []string{"shared"},
		DriveLinks:      map[string]string{"shared": "https://drive/shared"},
		ClipDetails:     map[string]scriptpkg.ClipDetail{"shared": {Name: "Shared", StartMs: 0, EndMs: 500}},
	}
	plan := &scriptpkg.ResolvedGenerationPlan{
		ClipEvidence: evidence,
		Segments: []scriptpkg.ScriptSegment{
			{ID: "one", Topic: "One", ClipIDs: []string{"shared"}},
			{ID: "two", Topic: "Two", ClipIDs: []string{"shared"}},
		},
	}
	input := adapters.ProcessInput{SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
		{ID: "one", Index: 0, Text: "One"},
		{ID: "two", Index: 1, Text: "Two"},
	}}}
	result, err := adapters.NewClipBindingsProcessor(zap.NewNop()).Process(context.Background(), plan, input)
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	for i, scene := range result.UpdatedSpecScene.Scenes {
		assertBindingIDs(t, scene.Bindings.Clips, []string{"shared"})
		if scene.Bindings.Clips[0].DriveLink != "https://drive/shared" {
			t.Errorf("scene[%d] lost shared DriveLink", i)
		}
	}
}

func assertBindingIDs(t *testing.T, bindings []scriptpkg.ClipBinding, want []string) {
	t.Helper()
	if len(bindings) != len(want) {
		t.Fatalf("binding count = %d, want %d (%v)", len(bindings), len(want), want)
	}
	for i, binding := range bindings {
		if binding.ClipID != want[i] {
			t.Errorf("binding[%d].ClipID = %q, want %q", i, binding.ClipID, want[i])
		}
	}
}
