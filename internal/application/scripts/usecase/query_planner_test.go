package usecase

import (
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
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
		if q == "Manos de Piedra biography" || q == "Manos de Piedra career achievements" {
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

func TestQueryPlanner_MetricAwareEstimatedNetWorth(t *testing.T) {
	planner := NewQueryPlannerForMetric(scriptpkg.RankingMetricEstimatedNetWorth)
	identity := scriptpkg.SubjectIdentity{CanonicalName: "Canelo Álvarez"}

	level0 := planner.Plan(identity, 0)
	if len(level0) == 0 || !containsQuery(level0, "Canelo Álvarez estimated net worth") {
		t.Fatalf("net-worth level 0 must include estimated net worth query, got %v", level0)
	}
	level1 := planner.Plan(identity, 1)
	if !containsQuery(level1, "Canelo Álvarez net worth Forbes") {
		t.Fatalf("net-worth level 1 must include Forbes query, got %v", level1)
	}
}

func TestQueryPlanner_MetricAwareCareerEarnings(t *testing.T) {
	planner := NewQueryPlannerForMetric(scriptpkg.RankingMetricCareerEarnings)
	identity := scriptpkg.SubjectIdentity{CanonicalName: "Mike Tyson"}

	level0 := planner.Plan(identity, 0)
	if !containsQuery(level0, "Mike Tyson career earnings") {
		t.Fatalf("career-earnings level 0 must include career earnings query, got %v", level0)
	}
}

func TestQueryPlanner_MetricAwareGenericIsSubjectAgnostic(t *testing.T) {
	planner := NewQueryPlannerForMetric(scriptpkg.RankingMetricGeneric)
	identity := scriptpkg.SubjectIdentity{CanonicalName: "Roberto Durán", Aliases: []string{"Manos de Piedra"}}

	level0 := planner.Plan(identity, 0)
	if !containsQuery(level0, "Roberto Durán biography") {
		t.Fatalf("generic level 0 must use the subject-agnostic biography query, got %v", level0)
	}
	level1 := planner.Plan(identity, 1)
	if !containsQuery(level1, "Manos de Piedra biography") {
		t.Fatalf("generic level 1 must use the subject-agnostic alias query, got %v", level1)
	}
	// No generic template may hardcode a domain qualifier.
	for _, level := range [][]string{level0, level1} {
		for _, q := range level {
			if strings.Contains(q, "boxing") || strings.Contains(q, "boxer") {
				t.Fatalf("generic query must not carry a hardcoded domain qualifier: %q", q)
			}
		}
	}
}

func TestQueryPlanner_MetricAffectsFullPlan(t *testing.T) {
	identity := scriptpkg.SubjectIdentity{CanonicalName: "Canelo Álvarez"}
	generic := NewQueryPlanner().FullPlan(identity, 4)
	netWorth := NewQueryPlannerForMetric(scriptpkg.RankingMetricEstimatedNetWorth).FullPlan(identity, 4)

	if len(netWorth) != 4 {
		t.Fatalf("metric-aware FullPlan returned %d queries, want 4", len(netWorth))
	}
	if strings.Join(generic, "\n") == strings.Join(netWorth, "\n") {
		t.Fatal("metric-aware FullPlan must differ from the generic plan")
	}
}

func containsQuery(queries []string, want string) bool {
	for _, q := range queries {
		if q == want {
			return true
		}
	}
	return false
}
