package images

import "testing"

func TestNormalizeLookupTerm(t *testing.T) {
	got := normalizeLookupTerm("Cus D’Amato")
	if got != "cus d amato" {
		t.Fatalf("unexpected normalized term: %q", got)
	}
}

func TestLooksLikeProperName(t *testing.T) {
	if !looksLikeProperName("Cus D’Amato") {
		t.Fatal("expected proper name detection for Cus D’Amato")
	}
	if looksLikeProperName("boxing ring") {
		t.Fatal("expected common phrase to not look like a proper name")
	}
}

func TestSelectBestWikidataHitPrefersExactMatch(t *testing.T) {
	hits := []struct {
		ID          string `json:"id"`
		Label       string `json:"label"`
		Description string `json:"description"`
	}{
		{ID: "Q1", Label: "Rosa D'Amato", Description: "politician"},
		{ID: "Q2", Label: "Cus D'Amato", Description: "boxing trainer"},
	}

	label, id, desc := selectBestWikidataHit("Cus D'Amato", hits)
	if id != "Q2" || label != "Cus D'Amato" || desc != "boxing trainer" {
		t.Fatalf("expected exact match, got label=%q id=%q desc=%q", label, id, desc)
	}
}

func TestSelectBestWikidataHitRejectsWeakSurnameMatch(t *testing.T) {
	hits := []struct {
		ID          string `json:"id"`
		Label       string `json:"label"`
		Description string `json:"description"`
	}{
		{ID: "Q1", Label: "Rosa D'Amato", Description: "politician"},
		{ID: "Q2", Label: "Other Person", Description: "politician"},
	}

	label, id, desc := selectBestWikidataHit("Cus D'Amato", hits)
	if label != "" || id != "" || desc != "" {
		t.Fatalf("expected no weak match, got label=%q id=%q desc=%q", label, id, desc)
	}
}

func TestSelectBestWikiTitlePrefersExactMatch(t *testing.T) {
	hits := []struct {
		Title string `json:"title"`
	}{
		{Title: "Rosa D'Amato"},
		{Title: "Cus D'Amato"},
	}

	got := selectBestWikiTitle("Cus D'Amato", hits)
	if got != "Cus D'Amato" {
		t.Fatalf("expected exact wiki title, got %q", got)
	}
}

func TestSelectBestWikiTitleRejectsWeakSurnameMatch(t *testing.T) {
	hits := []struct {
		Title string `json:"title"`
	}{
		{Title: "Rosa D'Amato"},
		{Title: "Michael Jackson"},
	}

	got := selectBestWikiTitle("Cus D'Amato", hits)
	if got != "" {
		t.Fatalf("expected no weak wiki title, got %q", got)
	}
}

func TestLooksLikeProperNameAndWikidataSelection(t *testing.T) {
	if !looksLikeProperName("Cus D'Amato") {
		t.Fatal("expected Cus D'Amato to be treated as a proper name")
	}
	if looksLikeProperName("boxing training") {
		t.Fatal("expected generic phrase to not be treated as a proper name")
	}

	hits := []struct {
		ID          string `json:"id"`
		Label       string `json:"label"`
		Description string `json:"description"`
	}{
		{ID: "Q1", Label: "Rosa D'Amato", Description: "politician"},
		{ID: "Q2", Label: "Cus D'Amato", Description: "boxing trainer"},
	}

	label, id, desc := selectBestWikidataHit("Cus D'Amato", hits)
	if id != "Q2" || label != "Cus D'Amato" || desc != "boxing trainer" {
		t.Fatalf("expected Cus D'Amato to win selection, got label=%q id=%q desc=%q", label, id, desc)
	}

	wikiHits := []struct {
		Title string `json:"title"`
	}{
		{Title: "Rosa D'Amato"},
		{Title: "Cus D'Amato"},
	}
	if got := selectBestWikiTitle("Cus D'Amato", wikiHits); got != "Cus D'Amato" {
		t.Fatalf("expected Cus D'Amato wiki title, got %q", got)
	}
}
