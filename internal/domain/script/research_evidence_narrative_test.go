package script

import "testing"

func TestResearchEvidenceNarrativePlanAssignsOneScenePerRank(t *testing.T) {
	pack := &ResearchEvidencePack{
		Version: "research-evidence-v1",
		Topic:   "boxing",
		Candidates: []RankedResearchCandidate{
			{CandidateID: "ali", Label: "Muhammad Ali", Rank: 1},
			{CandidateID: "tyson", Label: "Mike Tyson", Rank: 2},
			{CandidateID: "foreman", Label: "George Foreman", Rank: 3},
		},
	}

	got := pack.NarrativePlanInstructions()
	for _, want := range []string{
		"INTRO",
		"SCENE 1 — RANK #3 — George Foreman",
		"SCENE 2 — RANK #2 — Mike Tyson",
		"SCENE 3 — RANK #1 — Muhammad Ali",
		"CONCLUSION",
		"Each ranked boxer owns exactly one scene",
		"do not reuse another boxer as the subject",
	} {
		if !containsText(got, want) {
			t.Fatalf("narrative plan missing %q:\n%s", want, got)
		}
	}
}

func containsText(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
