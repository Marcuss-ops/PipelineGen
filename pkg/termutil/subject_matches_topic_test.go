package termutil

import "testing"

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
