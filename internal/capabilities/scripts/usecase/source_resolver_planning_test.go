package usecase

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestBuildSearchResolutionPlanDefaultsAndOverfetch(t *testing.T) {
	plan, err := buildSearchResolutionPlan(scriptpkg.SourceSpec{Query: "  climate  ", MaxClips: 3})
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if plan.Query != "climate" || plan.Limit != 3 || plan.SearchLimit != 20 {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestBuildSearchResolutionPlanRequiresQuery(t *testing.T) {
	if _, err := buildSearchResolutionPlan(scriptpkg.SourceSpec{}); err == nil {
		t.Fatal("expected missing query error")
	}
}

func TestResearchTopicAndFallbackTitle(t *testing.T) {
	if got := researchTopic(scriptpkg.SourceSpec{Query: "query"}); got != "query" {
		t.Fatalf("research topic = %q", got)
	}
	if got := fallbackTitle("", "query"); got != "query" {
		t.Fatalf("fallback title = %q", got)
	}
}
