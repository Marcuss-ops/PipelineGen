package entities

import "testing"

func TestCanonicalEntityIDPersonPossessiveUsesBaseIdentity(t *testing.T) {
	got := CanonicalEntityID("PERSON", "Donald Trump's")
	if got != "person:donald-trump" {
		t.Fatalf("possessive PERSON id = %q, want person:donald-trump", got)
	}
	if got := CanonicalEntityID("PERSON", "Donald Trump"); got != "person:donald-trump" {
		t.Fatalf("base PERSON id = %q, want person:donald-trump", got)
	}
}
