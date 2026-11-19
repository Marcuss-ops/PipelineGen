package overlays

import "testing"

func mustCandidate(t *testing.T, id, label, scope string) MapCandidate {
	t.Helper()
	candidate, ok := NewMapCandidate(id, label, 41.9, 12.5, 0, 1_000_000, 0.5, scope)
	if !ok {
		t.Fatalf("candidate %q (%s) did not validate", label, scope)
	}
	return candidate
}

func TestScopeFitsMapLevel(t *testing.T) {
	cases := []struct {
		scope, level string
		want         bool
	}{
		{"city", "municipality", true},
		{"locality", "municipality", true},
		{"town", "municipality", true},
		{"country", "municipality", false},
		{"nation", "country", true},
		{"state", "region", true},
		{"region", "municipality", false},
		{"", "municipality", false},
		{"country", "country", true},
		{"city", "country", false},
		{"region", "region", true},
		{"city", "region", true},
		{"continent", "region", false},
		{"continent", "continent", true},
		{"country", "continent", false},
		{"city", "", true},
		{"country", "auto", true},
	}
	for _, tc := range cases {
		if got := ScopeFitsMapLevel(tc.scope, tc.level); got != tc.want {
			t.Errorf("ScopeFitsMapLevel(%q, %q) = %t, want %t", tc.scope, tc.level, got, tc.want)
		}
	}
}

func TestFilterMapCandidatesByLevelKeepsOnlyTheLevelScope(t *testing.T) {
	candidates := []MapCandidate{
		mustCandidate(t, "jlm", "Gerusalemme", "city"),
		mustCandidate(t, "isr", "Israele", "country"),
		mustCandidate(t, "sea", "Mar Morto", ""),
		mustCandidate(t, "bet", "Betlemme", "city"),
	}
	kept := FilterMapCandidatesByLevel(candidates, "municipality")
	if len(kept) != 2 || kept[0].Label != "Gerusalemme" || kept[1].Label != "Betlemme" {
		t.Fatalf("municipality filter kept %v, want the two cities", labels(kept))
	}
	kept = FilterMapCandidatesByLevel(candidates, "country")
	if len(kept) != 1 || kept[0].Label != "Israele" {
		t.Fatalf("country filter kept %v, want only Israele", labels(kept))
	}
}

// A scene that only names countries cannot satisfy a municipality map. The
// hard filter returns no pin: substituting the country would put a nation on a
// city plate, which is worse than dropping the map item.
func TestFilterMapCandidatesByLevelReturnsEmptyWhenNothingMatches(t *testing.T) {
	candidates := []MapCandidate{
		mustCandidate(t, "isr", "Israele", "country"),
		mustCandidate(t, "sea", "Mar Morto", ""),
	}
	kept := FilterMapCandidatesByLevel(candidates, "municipality")
	if len(kept) != 0 {
		t.Fatalf("hard filter kept %v, want no city-level pin", labels(kept))
	}
}

func TestFilterMapCandidatesByLevelWithoutLevelKeepsEverything(t *testing.T) {
	candidates := []MapCandidate{
		mustCandidate(t, "jlm", "Gerusalemme", "city"),
		mustCandidate(t, "isr", "Israele", "country"),
	}
	for _, level := range []string{"", "auto"} {
		if kept := FilterMapCandidatesByLevel(candidates, level); len(kept) != len(candidates) {
			t.Fatalf("level %q filtered %v, want every candidate", level, labels(kept))
		}
	}
}

func labels(candidates []MapCandidate) []string {
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, candidate.Label)
	}
	return out
}
