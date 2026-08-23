package usecase

import "testing"

func TestResearchHitMatchesSubject(t *testing.T) {
	if !researchHitMatchesSubject("Sugar Ray Leonard", "Sugar Ray Leonard boxing career") {
		t.Fatal("expected matching boxer result")
	}
	if researchHitMatchesSubject("Sugar Ray Leonard", "Sugar chemical compound") {
		t.Fatal("accepted unrelated sugar result")
	}
	if researchHitMatchesSubject("Floyd Mayweather Jr.", "George Floyd five-year anniversary") {
		t.Fatal("accepted unrelated George Floyd result")
	}
}

func TestResearchHitMatchesSubjectNormalizesDiacritics(t *testing.T) {
	if !researchHitMatchesSubject("Roberto Duran", "Roberto Durán was a Panamanian professional boxer") {
		t.Fatal("accented subject should match ASCII candidate")
	}
}

func TestResearchCandidateSearchNameUsesCanonicalAccent(t *testing.T) {
	if got := researchCandidateSearchName("Roberto Duran"); got != "Roberto Durán" {
		t.Fatalf("canonical alias=%q", got)
	}
	if got := researchCandidateSearchName("Joe Frazier"); got != "Smokin' Joe Frazier" {
		t.Fatalf("nickname alias=%q", got)
	}
}
