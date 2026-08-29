package adapters

import (
	"testing"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestBuildYouTubeQueryPlanEmptyRequestStillValid(t *testing.T) {
	plan := buildYouTubeQueryPlan(scriptports.VidRushSearchRequest{})
	// With nothing to translate the plan is empty; Validate requires at
	// least one query, so callers must not send an empty plan to a backend.
	if len(plan.Queries) != 0 {
		t.Fatalf("empty request produced queries: %+v", plan.Queries)
	}
	if err := plan.Validate(); err == nil {
		t.Fatal("empty plan must fail Validate (no queries)")
	}
	if plan.Provider != scriptpkg.VidRushProviderYouTube {
		t.Fatalf("provider = %q, want youtube", plan.Provider)
	}
}

func TestBuildYouTubeQueryPlanCallerQueryIsExactSubject(t *testing.T) {
	plan := buildYouTubeQueryPlan(scriptports.VidRushSearchRequest{Query: "John Froelich tractor"})
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
	first := plan.Queries[0]
	if first.Query != "John Froelich tractor" || first.Intent != scriptports.QueryIntentExactSubject || first.Weight != 1.0 {
		t.Fatalf("first query = %+v, want exact_subject weight 1.0", first)
	}
}

func TestBuildYouTubeQueryPlanFullLadderFromProfile(t *testing.T) {
	plan := buildYouTubeQueryPlan(scriptports.VidRushSearchRequest{
		Query: "John Froelich tractor",
		SemanticProfile: &scriptpkg.SegmentSemanticProfile{
			Topic:            "first gasoline tractors",
			ImportantPhrases: []string{"John Froelich gasoline tractor"},
			Entities: []scriptpkg.ExtractedEntity{
				{Value: "John Froelich", Type: "PERSON", Confidence: 0.99},
				{Value: "Iowa", Type: "LOCATION", Confidence: 0.96},
				{Value: "1892", Type: "DATE", Confidence: 0.99},
			},
			VisualTerms: []scriptpkg.WeightedKeyword{
				{Value: "early gasoline tractor", Confidence: 0.9},
				{Value: "historic farm machinery", Confidence: 0.8},
			},
		},
	})
	if err := plan.Validate(); err != nil {
		t.Fatalf("plan invalid: %v", err)
	}
	if len(plan.Queries) < 3 || len(plan.Queries) > 5 {
		t.Fatalf("query count = %d, want 3-5", len(plan.Queries))
	}
	intents := map[scriptports.QueryIntent]int{}
	for _, q := range plan.Queries {
		intents[q.Intent]++
	}
	if intents[scriptports.QueryIntentExactSubject] == 0 {
		t.Fatalf("no exact_subject query in plan: %+v", plan.Queries)
	}
	if intents[scriptports.QueryIntentVisualFallback] == 0 {
		t.Fatalf("no visual_fallback query in plan: %+v", plan.Queries)
	}
	// Weights must be non-increasing (most specific first).
	for i := 1; i < len(plan.Queries); i++ {
		if plan.Queries[i].Weight > plan.Queries[i-1].Weight {
			t.Fatalf("weight order broken at %d: %+v", i, plan.Queries)
		}
	}
	// No phrase may be a giant concatenation: every query stays focused.
	for _, q := range plan.Queries {
		if words := len(splitWords(q.Query)); words > 8 {
			t.Fatalf("query %q too broad (%d words)", q.Query, words)
		}
	}
}

func TestBuildYouTubeQueryPlanDedupesAndCapsAtFive(t *testing.T) {
	plan := buildYouTubeQueryPlan(scriptports.VidRushSearchRequest{
		Query: "tractor",
		SemanticProfile: &scriptpkg.SegmentSemanticProfile{
			ImportantPhrases: []string{"tractor", "tractor", "farm machinery"},
			VisualTerms: []scriptpkg.WeightedKeyword{
				{Value: "tractor"}, {Value: "vintage tractor"}, {Value: "farm machinery"},
				{Value: "steam tractor"}, {Value: "field plowing"},
			},
		},
	})
	if err := plan.Validate(); err != nil {
		t.Fatalf("plan invalid: %v", err)
	}
	if len(plan.Queries) > 5 {
		t.Fatalf("queries = %d, want capped at 5", len(plan.Queries))
	}
	seen := map[string]bool{}
	for _, q := range plan.Queries {
		if seen[q.Query] {
			t.Fatalf("duplicate query %q in plan: %+v", q.Query, plan.Queries)
		}
		seen[q.Query] = true
	}
}

func TestBuildYouTubeQueryPlanPhrasesMatchesQueries(t *testing.T) {
	plan := buildYouTubeQueryPlan(scriptports.VidRushSearchRequest{Query: "steam tractor"})
	phrases := plan.Phrases()
	if len(phrases) != len(plan.Queries) {
		t.Fatalf("phrases = %v, want %d entries matching queries", phrases, len(plan.Queries))
	}
	for i := range phrases {
		if phrases[i] != plan.Queries[i].Query {
			t.Fatalf("phrases[%d] = %q, want %q", i, phrases[i], plan.Queries[i].Query)
		}
	}
}

func splitWords(s string) []string {
	out := []string{}
	cur := ""
	for _, r := range s {
		if r == ' ' || r == '\t' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
