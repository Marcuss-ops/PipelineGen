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

// ===== Pure function tests for search.go =====

func TestMinWikiScore_ProperName(t *testing.T) {
	if got := minWikiScore("Cus D'Amato"); got != 80 {
		t.Fatalf("expected 80 for proper name, got %d", got)
	}
}

func TestMinWikiScore_Generic(t *testing.T) {
	if got := minWikiScore("boxing ring"); got != 50 {
		t.Fatalf("expected 50 for generic query, got %d", got)
	}
}

func TestMinWikiScore_ShortQuery(t *testing.T) {
	if got := minWikiScore("a"); got != 50 {
		t.Fatalf("expected 50 for short query, got %d", got)
	}
}

func TestScoreWikiCandidate_ExactMatch(t *testing.T) {
	if got := scoreWikiCandidate("Cus D'Amato", "Cus D'Amato"); got != 100 {
		t.Fatalf("expected 100 for exact match, got %d", got)
	}
}

func TestScoreWikiCandidate_PrefixMatch(t *testing.T) {
	if got := scoreWikiCandidate("Cus D", "Cus D'Amato"); got != 95 {
		t.Fatalf("expected 95 for prefix match, got %d", got)
	}
}

func TestScoreWikiCandidate_PartialTokenMatch(t *testing.T) {
	score := scoreWikiCandidate("Elon Musk", "Elon Reeve Musk")
	// Tokens: "elon" matches, "musk" matches => matched=2, len(qTokens)=2
	// matched == len(qTokens) => 40 + 20*2 = 80
	// qTokens[0] == cTokens[0] => +10 = 90
	if score != 90 {
		t.Fatalf("expected 90 for two-token partial match, got %d", score)
	}
}

func TestScoreWikiCandidate_NoMatch(t *testing.T) {
	// "Cus D'Amato" tokens: ["cus", "amato"] — "d" removed (len<2), "'" regex-replaced
	// "Rosa D'Amato" tokens: ["rosa", "amato"]
	// "amato" matches => scored 20, but minWikiScore("Cus D'Amato")=80 would reject it
	if got := scoreWikiCandidate("Cus D'Amato", "Rosa D'Amato"); got != 20 {
		t.Fatalf("expected 20 for shared token match (rejected by threshold upstream), got %d", got)
	}
}

func TestScoreWikiCandidate_EmptyTokens(t *testing.T) {
	if got := scoreWikiCandidate("", "anything"); got != 0 {
		t.Fatalf("expected 0 for empty query, got %d", got)
	}
}

func TestScoreWikiCandidate_SingleToken(t *testing.T) {
	// single token match with bonus
	score := scoreWikiCandidate("musk", "Elon Musk")
	// "musk" matches one token in candidate => matched=1, len(qTokens)=1
	// 20*1 + 25 (single token bonus) = 45
	if score != 45 {
		t.Fatalf("expected 45 for single token match, got %d", score)
	}
}

func TestScoreWikiCandidate_SingleTokenNoMatch(t *testing.T) {
	if got := scoreWikiCandidate("xylophone", "Elon Musk"); got != 0 {
		t.Fatalf("expected 0 for single token no match, got %d", got)
	}
}

func TestMeaningfulLookupTokens_RemovesStopwords(t *testing.T) {
	tokens := meaningfulLookupTokens("The quick brown fox")
	if len(tokens) != 3 {
		t.Fatalf("expected 3 tokens after removing 'the', got %v", tokens)
	}
	if tokens[0] != "quick" || tokens[1] != "brown" || tokens[2] != "fox" {
		t.Fatalf("unexpected tokens: %v", tokens)
	}
}

func TestMeaningfulLookupTokens_ItalianStopwords(t *testing.T) {
	tokens := meaningfulLookupTokens("Della Rosa dei Venti")
	// "della" is a stopword, "dei" is NOT in the stopword list, "venti" kept
	expected := []string{"rosa", "dei", "venti"}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, tokens)
	}
	for i, tok := range tokens {
		if tok != expected[i] {
			t.Fatalf("token %d: expected %q, got %q", i, expected[i], tok)
		}
	}
}

func TestMeaningfulLookupTokens_ShortTokensRemoved(t *testing.T) {
	tokens := meaningfulLookupTokens("a an the cat")
	// "a" len<2 skip, "an" kept (len>=2, not in stopwords), "the" stopword, "cat" kept
	expected := []string{"an", "cat"}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, tokens)
	}
	for i, tok := range tokens {
		if tok != expected[i] {
			t.Fatalf("token %d: expected %q, got %q", i, expected[i], tok)
		}
	}
}

func TestMeaningfulLookupTokens_SpecialChars(t *testing.T) {
	tokens := meaningfulLookupTokens("Cus D'Amato")
	// "'" is not [a-z0-9] so "d'amato" -> "d amato", "d" is len<2 and removed
	expected := []string{"cus", "amato"}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, tokens)
	}
	for i, tok := range tokens {
		if tok != expected[i] {
			t.Fatalf("token %d: expected %q, got %q", i, expected[i], tok)
		}
	}
}

func TestMeaningfulLookupTokens_EmptyInput(t *testing.T) {
	if got := meaningfulLookupTokens(""); got != nil {
		t.Fatalf("expected nil for empty input, got %v", got)
	}
}
