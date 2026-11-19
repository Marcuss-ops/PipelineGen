package overlays

import "testing"

func TestDedupeMapCandidatesByArticleKeepsLongerOccurrence(t *testing.T) {
	candidates := []MapCandidate{
		{EntityID: "ent-francia-article", Label: "La Francia", DurationUS: 96_000, Score: 0.3},
		{EntityID: "ent-italia", Label: "Italia", DurationUS: 730_000, Score: 0.3},
		{EntityID: "ent-francia", Label: "Francia", DurationUS: 4_182_000, Score: 0.3},
		{EntityID: "ent-italia-article", Label: "L'Italia", DurationUS: 400_000, Score: 0.3},
	}
	got := dedupeMapCandidatesByArticle(candidates)
	if len(got) != 2 {
		t.Fatalf("dedupe returned %d candidates, want 2: %#v", len(got), got)
	}
	byLabel := map[string]MapCandidate{}
	for _, candidate := range got {
		byLabel[candidate.Label] = candidate
	}
	if candidate := byLabel["Italia"]; candidate.DurationUS != 730_000 {
		t.Errorf("Italia candidate = %#v, want the longer occurrence", candidate)
	}
	if candidate := byLabel["Francia"]; candidate.DurationUS != 4_182_000 {
		t.Errorf("Francia candidate = %#v, want the longer occurrence", candidate)
	}
}
