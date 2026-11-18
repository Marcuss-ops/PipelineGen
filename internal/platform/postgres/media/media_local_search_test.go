package media

import (
	"strings"
	"testing"
)

// TestBuildLocalMediaSearchQuery_LanguageFilter pins the predicate that makes
// q.Filters.Language a real SQL filter on the PostgreSQL local path (the
// SQLite media mirror was retired). Before TICKET-MEDIA-ASSETS-LANGUAGE-COLUMN
// the value reached the backend struct and was dropped here.
func TestBuildLocalMediaSearchQuery_LanguageFilter(t *testing.T) {
	query, args := buildLocalMediaSearchQuery(LocalMediaSearchRequest{
		Text:     "greek salad",
		Language: "it",
		Limit:    10,
	})

	if !strings.Contains(query, "AND ($8 = '' OR language = $8)") {
		t.Fatalf("language predicate missing from query:\n%s", query)
	}
	if len(args) < 9 {
		t.Fatalf("args = %v, want at least 9 (language + limit)", args)
	}
	if got := args[7]; got != "it" {
		t.Fatalf("language arg = %v, want %q", got, "it")
	}
	if got := args[len(args)-1]; got != 10 {
		t.Fatalf("limit arg = %v, want 10", got)
	}
}

// TestBuildLocalMediaSearchQuery_EmptyLanguageMeansNoFilter pins the documented
// empty-value contract: the clause stays neutral, so a query without a
// language returns rows in every language rather than only rows with ”.
func TestBuildLocalMediaSearchQuery_EmptyLanguageMeansNoFilter(t *testing.T) {
	query, args := buildLocalMediaSearchQuery(LocalMediaSearchRequest{Text: "x"})

	if !strings.Contains(query, "AND ($8 = '' OR language = $8)") {
		t.Fatalf("language predicate missing from query:\n%s", query)
	}
	if got := args[7]; got != "" {
		t.Fatalf("language arg = %v, want empty (no filter)", got)
	}
}

// TestBuildLocalMediaSearchQuery_AllTermsKeepPositionalContract guards the
// dynamic placeholder arithmetic: adding the language parameter must not shift
// the per-term clauses or the LIMIT placeholder.
func TestBuildLocalMediaSearchQuery_AllTermsKeepPositionalContract(t *testing.T) {
	query, args := buildLocalMediaSearchQuery(LocalMediaSearchRequest{
		Text:     "a",
		AllTerms: []string{"b", "c"},
		Language: "en",
		Limit:    5,
	})

	if !strings.Contains(query, "LIKE $9") || !strings.Contains(query, "LIKE $10") {
		t.Fatalf("per-term placeholders shifted:\n%s", query)
	}
	if !strings.Contains(query, "LIMIT $11") {
		t.Fatalf("limit placeholder should be $11:\n%s", query)
	}
	if len(args) != 11 {
		t.Fatalf("args length = %d, want 11: %v", len(args), args)
	}
}
