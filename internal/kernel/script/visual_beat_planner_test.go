package script

import "testing"

func TestVisualBeatPlannerDividesSegmentByTargetDuration(t *testing.T) {
	planner, err := NewVisualBeatPlanner(VisualBeatPolicy{MinBeatMs: 4000, TargetBeatMs: 7500, MaxBeatMs: 12000})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planner.Plan("segment-1", 24800, []SegmentVisualBlock{
		{Text: "horses in fields"},
		{Text: "steam machinery"},
		{Text: "gasoline tractor"},
		{Text: "modern farming"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Beats) != 3 {
		t.Fatalf("beats = %d, want 3", len(plan.Beats))
	}
	if plan.Beats[0].StartMs != 0 || plan.Beats[len(plan.Beats)-1].EndMs != 24800 {
		t.Fatalf("plan does not cover full duration: %+v", plan.Beats)
	}
	for i, beat := range plan.Beats {
		if beat.ID == "" || beat.Position != i || beat.EndMs <= beat.StartMs {
			t.Fatalf("invalid beat %d: %+v", i, beat)
		}
		if i > 0 && beat.StartMs != plan.Beats[i-1].EndMs {
			t.Fatalf("gap before beat %d", i)
		}
	}
}

func TestVisualBeatPlannerKeepsShortSegmentAsOneBeat(t *testing.T) {
	planner := VisualBeatPlanner{Policy: VisualBeatPolicy{MinBeatMs: 4000, TargetBeatMs: 7500, MaxBeatMs: 12000}}
	plan, err := planner.Plan("segment-short", 3200, []SegmentVisualBlock{{Text: "short visual"}, {Text: "second visual"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Beats) != 1 || plan.Beats[0].StartMs != 0 || plan.Beats[0].EndMs != 3200 {
		t.Fatalf("short plan = %+v, want one exact-duration beat", plan.Beats)
	}
}

func TestVisualBeatPlannerNormalizesPolicyDefaults(t *testing.T) {
	policy, err := NormalizeVisualBeatPolicy(VisualBeatPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if policy != DefaultVisualBeatPolicy {
		t.Fatalf("policy = %+v, want defaults %+v", policy, DefaultVisualBeatPolicy)
	}
	if _, err := NewVisualBeatPlanner(VisualBeatPolicy{MinBeatMs: 9000, TargetBeatMs: 7500, MaxBeatMs: 12000}); err == nil {
		t.Fatal("expected invalid min/target policy error")
	}
}

func TestVisualBeatPlannerPlanWithBudgetUsesExactTimingSource(t *testing.T) {
	planner := VisualBeatPlanner{Policy: DefaultVisualBeatPolicy}
	plan, err := planner.PlanWithBudget(VisualTimingBudget{SegmentID: "segment-budget", DurationMs: 17500, Source: "voiceover"}, []SegmentVisualBlock{
		{Text: "first beat"}, {Text: "second beat"}, {Text: "third beat"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.DurationMs != 17500 || len(plan.Beats) != 2 {
		t.Fatalf("plan = %+v, want 17500ms and 2 beats", plan)
	}
	if plan.Beats[0].StartMs != 0 || plan.Beats[len(plan.Beats)-1].EndMs != 17500 {
		t.Fatalf("beats do not cover budget: %+v", plan.Beats)
	}
}

func TestVisualBeatPlannerUsesProfileForEmptyBlockText(t *testing.T) {
	planner := VisualBeatPlanner{Policy: DefaultVisualBeatPolicy}
	plan, err := planner.Plan("segment-profile", 8000, []SegmentVisualBlock{{
		SemanticProfile: SegmentSemanticProfile{Topic: "early gasoline tractor"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Beats) != 1 || plan.Beats[0].Text != "early gasoline tractor" {
		t.Fatalf("profile beat = %+v", plan.Beats)
	}
	if plan.Beats[0].SemanticProfile.Topic != "early gasoline tractor" {
		t.Fatalf("profile not propagated: %+v", plan.Beats[0].SemanticProfile)
	}
}
