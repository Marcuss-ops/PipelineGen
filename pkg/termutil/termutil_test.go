package termutil

import (
	"reflect"
	"testing"
)

func TestTermsFromText(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		opts     TermOptions
		expected []string
	}{
		{"empty text", "", TermOptions{}, nil},
		{"basic tokens", "hello world", TermOptions{MinLen: 1, Lowercase: true, RemoveStops: false, Unique: false}, []string{"hello", "world"}},
		{"min length filter", "a big cat", TermOptions{MinLen: 3, Lowercase: true, RemoveStops: false, Unique: false}, []string{"big", "cat"}},
		{"unique", "hello hello world", TermOptions{MinLen: 1, Lowercase: true, RemoveStops: false, Unique: true}, []string{"hello", "world"}},
		{"limit", "one two three four", TermOptions{MinLen: 1, Lowercase: true, RemoveStops: false, Unique: false, Limit: 2}, []string{"one", "two"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TermsFromText(tt.text, tt.opts)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("TermsFromText(%q, %+v) = %v, want %v", tt.text, tt.opts, got, tt.expected)
			}
		})
	}
}

func TestTermsFromFields(t *testing.T) {
	got := TermsFromFields("hello world", "", "world again")
	if len(got) == 0 {
		t.Fatal("TermsFromFields() returned empty slice")
	}
	foundHello := false
	for _, w := range got {
		if w == "hello" || w == "world" || w == "again" {
			foundHello = true
		}
	}
	if !foundHello {
		t.Errorf("TermsFromFields() = %v, expected at least one of hello/world/again", got)
	}
}

func TestCleanTerms(t *testing.T) {
	input := []string{"  Hello  ", "hello", "WORLD", "cat"}
	opts := TermOptions{MinLen: 3, Lowercase: true, RemoveStops: false, Unique: true}
	got := CleanTerms(input, opts)
	want := []string{"hello", "world", "cat"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CleanTerms(%v, ...) = %v, want %v", input, got, want)
	}
}

func TestLooksLikePersonName(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"empty", "", false},
		{"single capitalized", "John", true},
		{"full name", "John Smith", true},
		{"lowercase", "john", false},
		{"four words capitalized", "John Jacob Jingleheimer Schmidt", true},
		{"five words", "John Jacob Jingleheimer Schmidt Extra", false},
		{"mixed case sentence", "The Quick Brown Fox", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LooksLikePersonName(tt.text)
			if got != tt.want {
				t.Errorf("LooksLikePersonName(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

func TestExtractLikelyNames(t *testing.T) {
	got := ExtractLikelyNames("John and Mary went to Paris. John saw Mary.")
	want := []string{"John", "Mary", "Paris"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExtractLikelyNames() = %v, want %v", got, want)
	}
}

func TestSubjectMatchesTopic(t *testing.T) {
	if !SubjectMatchesTopic("art history", []string{"art"}) {
		t.Error("expected match")
	}
	if SubjectMatchesTopic("science", []string{"art"}) {
		t.Error("expected no match")
	}
	if !SubjectMatchesTopic("anything", []string{}) {
		t.Error("expected match with empty topic tokens")
	}
}

func TestTopicTokens(t *testing.T) {
	got := TopicTokens("Hello, World!")
	want := []string{"hello", "world"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("TopicTokens() = %v, want %v", got, want)
	}
}

func TestConciseSubject(t *testing.T) {
	if got := ConciseSubject("a b c d e f g"); got != "a b c d e" {
		t.Errorf("ConciseSubject() = %q, want %q", got, "a b c d e")
	}
	if got := ConciseSubject(""); got != "" {
		t.Errorf("ConciseSubject(empty) = %q, want %q", got, "")
	}
}

func TestPreferredEntitySubject(t *testing.T) {
	entities := []string{"Art History", "Science", "Computer Science"}
	subject := "History of Art"
	tokens := []string{"art", "history"}
	got := PreferredEntitySubject(entities, subject, tokens)
	if got == "" {
		t.Error("expected a match, got empty")
	}
	if got != "Art History" {
		t.Errorf("PreferredEntitySubject() = %q, want %q", got, "Art History")
	}
}
