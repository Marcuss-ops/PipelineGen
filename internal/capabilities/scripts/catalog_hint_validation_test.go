package scriptgeneration

import (
	"os"
	"strings"
	"testing"

	phrasepkg "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/phrases"
)

// Validates every sentence in ops/jobs/longsents.txt survives the
// operator-hint ingress filter (proper-name gate) AND the grounding gate
// (verbatim substring of its own narration + no entity overlap).
func TestCatalogHintsAllSurvive(t *testing.T) {
	raw, err := os.ReadFile("../../../ops/jobs/longsents.txt")
	if err != nil {
		t.Fatalf("read sents: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 68 {
		t.Fatalf("want 68 sentences, got %d", len(lines))
	}
	fail := 0
	for i, s := range lines {
		s = strings.TrimSpace(s)
		if phrasepkg.ContainsProperNamePair(s) {
			t.Errorf("line %d blocked by proper-name gate: %q", i, s)
			fail++
			continue
		}
		got := groundImportantPhrases(s, nil, []string{s}, 1)
		if len(got) != 1 || got[0] != s {
			t.Errorf("line %d blocked by grounding gate: %q -> %q", i, s, got)
			fail++
		}
	}
	if fail > 0 {
		t.Fatalf("%d/68 sentences blocked", fail)
	}
}
