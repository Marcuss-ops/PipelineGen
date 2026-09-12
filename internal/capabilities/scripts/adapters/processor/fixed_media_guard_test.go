// Package processor — fixed_media_guard_test.go
//
// The fixed-media downstream guard is split across two packages: the
// scene-execution filters and the visual-planning half live in the parent
// `adapters` package (scene_execution_test.go), while the image-generation
// half belongs to the processor that implements it. This file pins the
// processor-owned half so the guard keeps both sides covered after the
// implementation moved out of `adapters`.
package processor

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func fixedMediaScene() scriptpkg.SpecScene {
	return scriptpkg.SpecScene{
		ID: "intro", SegmentID: "intro-segment", Index: 0, Text: "authoritative intro",
		Kind: scriptpkg.SceneIntro, ExecutionMode: scriptpkg.SceneExecutionFixedMedia,
		Bindings: scriptpkg.SceneBindings{Clip: &scriptpkg.ClipBinding{ClipID: "intro-clip"}},
	}
}

// TestFixedMediaDoesNotEnterImageGeneration pins the processor-owned half of
// the fixed-media guard: a scene marked SceneExecutionFixedMedia must never
// reach image generation.
func TestFixedMediaDoesNotEnterImageGeneration(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{ID: "job", MediaPlan: mediadomain.MediaPlanSpec{Mode: mediadomain.MediaPlanModeHybrid}}
	input := adapters.ProcessInput{SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{fixedMediaScene()}}}

	imageResult, err := NewImageProcessor(nil, nil).Process(context.Background(), plan, input)
	if err != nil {
		t.Fatal(err)
	}
	if imageResult.Changed || len(imageResult.SceneImages) != 0 {
		t.Fatalf("fixed scene entered image generation: %+v", imageResult)
	}
}
