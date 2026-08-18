package usecase

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func TestQueryPlanner_PlanLevel0(t *testing.T) {
	planner := NewQueryPlanner()
	identity := scriptpkg.SubjectIdentity{
		CanonicalName: "Roberto Durán",
		Aliases:       []string{"Manos de Piedra", "El Roberto"},
	}

	queries := planner.Plan(identity, 0)
	if len(queries) < 2 {
		t.Fatalf("Plan(level=0) returned %d queries, want >= 2", len(queries))
	}
	for _, q := range queries {
		if q == "" {
			t.Error("Plan(level=0) returned empty query")
		}
		t.Logf("Level 0: %s", q)
	}
}

func TestQueryPlanner_PlanLevel1UsesAlias(t *testing.T) {
	planner := NewQueryPlanner()
	identity := scriptpkg.SubjectIdentity{
		CanonicalName: "Roberto Durán",
		Aliases:       []string{"Manos de Piedra"},
	}

	queries := planner.Plan(identity, 1)
	if len(queries) == 0 {
		t.Fatal("Plan(level=1) returned 0 queries")
	}
	// At least one query should reference the alias
	foundAlias := false
	for _, q := range queries {
		if q == "Manos de Piedra boxing earnings" || q == "Manos de Piedra biography championships" {
			foundAlias = true
		}
	}
	if !foundAlias {
		t.Errorf("Plan(level=1) should use alias, got %v", queries)
	}
}

func TestQueryPlanner_FullPlan(t *testing.T) {
	planner := NewQueryPlanner()
	identity := scriptpkg.SubjectIdentity{
		CanonicalName: "Mike Tyson",
		Aliases:       []string{"Iron Mike"},
	}

	queries := planner.FullPlan(identity, 6)
	if len(queries) != 6 {
		t.Errorf("FullPlan(max=6) returned %d queries, want 6", len(queries))
	}
	// Check uniqueness
	seen := make(map[string]bool)
	for _, q := range queries {
		if seen[q] {
			t.Errorf("FullPlan returned duplicate query: %q", q)
		}
		seen[q] = true
	}
}

func TestQueryPlanner_FullPlanMaxZero(t *testing.T) {
	planner := NewQueryPlanner()
	identity := scriptpkg.SubjectIdentity{CanonicalName: "Test"}
	queries := planner.FullPlan(identity, 0)
	if len(queries) != 4 {
		t.Errorf("FullPlan(max=0) returned %d queries, want 4 (default)", len(queries))
	}
}

func TestQueryPlanner_FullPlanEmptyIdentity(t *testing.T) {
	planner := NewQueryPlanner()
	identity := scriptpkg.SubjectIdentity{}
	queries := planner.FullPlan(identity, 4)
	if len(queries) != 0 {
		t.Errorf("FullPlan(empty identity) returned %d queries, want 0", len(queries))
	}
}

func TestQueryPlanner_NewConstructor(t *testing.T) {
	p := NewQueryPlanner()
	if p == nil {
		t.Fatal("NewQueryPlanner() returned nil")
	}
}
