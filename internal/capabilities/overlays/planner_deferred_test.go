package overlays

import (
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

func TestBuildPlanCandidatesDefersAdmissionAndFinalizes(t *testing.T) {
	input := PlanInput{
		PlanID:  "deferred-plan",
		VideoID: "deferred-video",
		Width:   1280,
		Height:  720,
		FPSNum:  30,
		FPSDen:  1,
		Scenes: []SceneInput{{
			ID: "scene-1",
			Phrases: []TimedAnnotation{
				{Text: "first grounded phrase", StartMs: 0, EndMs: 1000, StartUS: 0, DurationUS: 1_000_000, Score: 0.9},
				{Text: "second grounded phrase", StartMs: 1100, EndMs: 2200, StartUS: 1_100_000, DurationUS: 1_100_000, Score: 0.8},
			},
		}},
	}
	config := PlannerConfig{
		MaxPhrases:                 2,
		MaxPhraseWords:             10,
		MaxOverlap:                 10,
		RunLevelEditorialBudget:    true,
		RunLevelPhraseOverlayLimit: 1,
	}

	legacy, err := BuildPlan(input, config)
	if err != nil {
		t.Fatalf("legacy build: %v", err)
	}
	if len(legacy.Items) != 1 {
		t.Fatalf("legacy admission kept %d phrases, want 1", len(legacy.Items))
	}

	deferred, err := BuildPlanCandidates(input, config)
	if err != nil {
		t.Fatalf("candidate build: %v", err)
	}
	if len(deferred.Plan.Items) != 2 {
		t.Fatalf("candidate build kept %d phrases, want both", len(deferred.Plan.Items))
	}
	for _, item := range deferred.Plan.Items {
		if item.MotionID != "" {
			t.Fatalf("candidate item %q already has motion %q", item.ID, item.MotionID)
		}
	}

	final, err := FinalizeDeferredPlan(deferred)
	if err != nil {
		t.Fatalf("finalize deferred plan: %v", err)
	}
	if len(final.Items) != 2 {
		t.Fatalf("finalize dropped candidates: got %d, want 2", len(final.Items))
	}
	if err := final.Validate(); err != nil {
		t.Fatalf("final plan invalid: %v", err)
	}
	for _, item := range final.Items {
		if item.MotionID == "" {
			t.Fatalf("final item %q has no motion", item.ID)
		}
	}
}

func TestFinalizeDeferredPlanKeepsExplicitEmptySelection(t *testing.T) {
	deferred := DeferredPlan{
		Plan: capmodel.OverlayPlan{
			SchemaVersion: capmodel.SchemaVersionPlan,
			PlanID:        "empty-plan",
			VideoID:       "empty-video",
			Width:         1280,
			Height:        720,
			FPSNum:        30,
			FPSDen:        1,
			Items:         nil,
		},
		Input: PlanInput{
			PlanID:  "empty-plan",
			VideoID: "empty-video",
			Width:   1280,
			Height:  720,
			FPSNum:  30,
			FPSDen:  1,
		},
	}
	final, err := FinalizeDeferredPlan(deferred)
	if err != nil {
		t.Fatalf("finalize empty deferred plan: %v", err)
	}
	if len(final.Items) != 0 {
		t.Fatalf("empty selection gained %d items", len(final.Items))
	}
}
