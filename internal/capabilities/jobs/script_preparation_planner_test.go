package jobs

import (
	"context"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

func TestScriptPreparationPlanner_BuildsAllPreparationPhases(t *testing.T) {
	planner := NewScriptPreparationPlanner()
	plan, err := planner.Plan(context.Background(), &job.Job{ID: "job-script-1", Type: job.TypeScriptGenerate, Payload: []byte(`{"title":"test"}`)})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	want := []string{"PREFLIGHT", "SOURCE", "RESEARCH", "LLM", "SCENE_FANOUT", "NLP", "VIDRUSH", "TTS", "OVERLAY", "AUDIO", "DOCUMENTS"}
	seen := make(map[string]bool)
	for _, unit := range plan.Units {
		seen[unit.Kind] = true
	}
	for _, phase := range want {
		if !seen[phase] {
			t.Errorf("missing preparation phase %q", phase)
		}
	}
	if len(plan.Units) != 14 {
		t.Errorf("unit count = %d, want 14", len(plan.Units))
	}
}

func TestScriptPreparationPlanner_UsesDependenciesAndStableFingerprints(t *testing.T) {
	planner := NewScriptPreparationPlanner()
	input := &job.Job{ID: "job-script-1", Type: job.TypeScriptGenerate, Payload: []byte(`{"title":"test"}`)}
	first, err := planner.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := planner.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Units) != len(second.Units) {
		t.Fatalf("plan lengths differ: %d != %d", len(first.Units), len(second.Units))
	}
	for i := range first.Units {
		if first.Units[i].Fingerprint != second.Units[i].Fingerprint {
			t.Errorf("unit %q fingerprint changed between identical plans", first.Units[i].ID)
		}
	}
	byID := make(map[string]PreparationUnit)
	for _, unit := range first.Units {
		byID[unit.ID] = unit
	}
	for _, dependency := range []struct{ unit, depends string }{
		{"request.validate", "request.parse"},
		{"source.resolve", "request.normalize"},
		{"narrative.plan", "source.resolve"},
		{"scene.overlay", "scene.tts"},
		{"audio.prepare", "scene.tts"},
	} {
		found := false
		for _, dep := range byID[dependency.unit].DependsOn {
			if dep == dependency.depends {
				found = true
			}
		}
		if !found {
			t.Errorf("unit %q missing dependency %q", dependency.unit, dependency.depends)
		}
	}
}

func TestComposeJobPreparationRegistry_UsesScriptPlanner(t *testing.T) {
	registry, err := ComposeJobPreparationRegistry()
	if err != nil {
		t.Fatal(err)
	}
	planner, ok := registry.Resolve(job.TypeScriptGenerate)
	if !ok {
		t.Fatal("script.generate planner not registered")
	}
	plan, err := planner.Plan(context.Background(), &job.Job{ID: "job-script-2", Type: job.TypeScriptGenerate})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Units) != 14 {
		t.Fatalf("registered script planner produced %d units, want 14", len(plan.Units))
	}
}
