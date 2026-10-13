package overlays

import (
	"testing"
)

// Dedupe regression: a repeated wording in one scene must not mint two
// overlay items with the same id (plan validation fails fail-closed on
// duplicates). The first — highest-priority — occurrence wins.
func TestDedupeCandidateIDsKeepsFirstOccurrence(t *testing.T) {
	in := []TimedAnnotation{
		{Text: "Segnato profondamente la comunità", StartMs: 100, EndMs: 900, Score: 2},
		{Text: "Unaltra frase diversa", StartMs: 1000, EndMs: 1200, Score: 1},
		{Text: "segnato PROfONDAMENTE la comunità!", StartMs: 1300, EndMs: 1500, Score: 0.5},
	}
	got := dedupeCandidateIDs(in, "scene-0", "phrase")
	if len(got) != 2 {
		t.Fatalf("dedupeCandidateIDs kept %d candidates, want 2", len(got))
	}
	if got[0].Text != in[0].Text || got[1].Text != in[1].Text {
		t.Fatalf("dedupeCandidateIDs kept %+v, want first two", got)
	}
	seen := map[string]struct{}{}
	for _, c := range got {
		id := itemID("scene-0", "phrase", c.Text)
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate id %q after dedupe", id)
		}
		seen[id] = struct{}{}
	}
}
