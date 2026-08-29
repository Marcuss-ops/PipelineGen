package jobs

import (
	"context"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

type preparationPlannerFunc func(context.Context, *job.Job) (PreparationPlan, error)

func (f preparationPlannerFunc) Plan(ctx context.Context, j *job.Job) (PreparationPlan, error) {
	return f(ctx, j)
}

func TestJobPreparationRegistry_ResolvesAndDispatchesPlanner(t *testing.T) {
	registry := NewJobPreparationRegistry()
	called := false
	planner := preparationPlannerFunc(func(_ context.Context, j *job.Job) (PreparationPlan, error) {
		called = true
		return PreparationPlan{Units: []PreparationUnit{{ID: "normalize", Kind: "normalize", Fingerprint: "test-fingerprint"}}, JobID: j.ID}, nil
	})

	if err := RegisterPreparationPlanner(registry, "script.generate", planner); err != nil {
		t.Fatalf("RegisterPreparationPlanner: %v", err)
	}

	got, err := registry.Plan(context.Background(), &job.Job{ID: "job-1", Type: "script.generate"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !called {
		t.Fatal("registered planner was not invoked")
	}
	if got.JobID != "job-1" || len(got.Units) != 1 || got.Units[0].ID != "normalize" {
		t.Fatalf("unexpected plan: %#v", got)
	}
}

func TestJobPreparationRegistry_RejectsDuplicateAndUnknownTypes(t *testing.T) {
	registry := NewJobPreparationRegistry()
	planner := preparationPlannerFunc(func(context.Context, *job.Job) (PreparationPlan, error) {
		return PreparationPlan{}, nil
	})
	if err := registry.Register("clip.render", planner); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := registry.Register("clip.render", planner); err == nil {
		t.Fatal("duplicate Register unexpectedly succeeded")
	}
	if _, err := registry.Plan(context.Background(), &job.Job{Type: "unknown"}); err == nil {
		t.Fatal("Plan for unknown type unexpectedly succeeded")
	}
}

func TestJobPreparationRegistry_FreezeRejectsNewRegistrations(t *testing.T) {
	registry := NewJobPreparationRegistry()
	registry.Freeze()
	if !registry.IsFrozen() {
		t.Fatal("registry should be frozen")
	}
	planner := preparationPlannerFunc(func(context.Context, *job.Job) (PreparationPlan, error) {
		return PreparationPlan{}, nil
	})
	if err := registry.Register("voiceover.generate", planner); err == nil {
		t.Fatal("Register after Freeze unexpectedly succeeded")
	}
}

func TestComposeJobPreparationRegistry_RegistersKnownTypes(t *testing.T) {
	registry, err := ComposeJobPreparationRegistry()
	if err != nil {
		t.Fatalf("ComposeJobPreparationRegistry: %v", err)
	}
	for _, jobType := range []string{
		job.TypeScriptGenerate,
		job.TypeScriptGenerateItem,
		job.TypeVoiceoverGenerate,
		job.TypeVoiceoverBatch,
		job.TypeVoiceoverGenerateItem,
		job.TypeVoiceoverPromo,
		job.TypeYouTubeClipExtract,
		job.TypeClipRender,
	} {
		if _, ok := registry.Resolve(jobType); !ok {
			t.Errorf("known job type %q has no preparation planner", jobType)
		}
	}
	if !registry.IsFrozen() {
		t.Fatal("composed preparation registry should be frozen")
	}
}

func TestJobPreparationRegistry_AllTypesAreSorted(t *testing.T) {
	registry := NewJobPreparationRegistry()
	planner := preparationPlannerFunc(func(context.Context, *job.Job) (PreparationPlan, error) {
		return PreparationPlan{}, nil
	})
	for _, jobType := range []string{"youtube.extract", "clip.render", "script.generate"} {
		if err := registry.Register(jobType, planner); err != nil {
			t.Fatalf("Register %s: %v", jobType, err)
		}
	}
	got := registry.AllTypes()
	want := []string{"clip.render", "script.generate", "youtube.extract"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("AllTypes = %#v, want %#v", got, want)
		}
	}
}
