package entities

import "testing"

// TestCanonicalEntitySlugIsTheInverseOfCanonicalEntityID pins the ONE
// derivation the canonical image library uses for its per-image Drive folder
// (<ImagesRootFolder>/<slug>/<file>). A consumer that split "type:slug" itself
// would be a second owner of the identity format and could drift from the id.
func TestCanonicalEntitySlugIsTheInverseOfCanonicalEntityID(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"person:michael-jordan", "michael-jordan"},
		{"PERSON:michael-jordan", "michael-jordan"},
		{"  person:michael-jordan  ", "michael-jordan"},
		{"organization:acme-corp", "acme-corp"},
		{"michael-jordan", "michael-jordan"}, // already a slug
		{"", ""},
		{"   ", ""},
	} {
		if got := CanonicalEntitySlug(tc.in); got != tc.want {
			t.Fatalf("CanonicalEntitySlug(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// The id → slug round trip is what makes the library folder stable for every
	// spelling of the same entity.
	for _, variant := range []string{"Michael Jordan", "MICHAEL JORDAN", "  Michael   Jordan  "} {
		if got := CanonicalEntitySlug(CanonicalEntityID("PERSON", variant)); got != "michael-jordan" {
			t.Fatalf("round trip for %q = %q, want michael-jordan", variant, got)
		}
	}
}

func TestCanonicalEntityIDPersonPossessiveUsesBaseIdentity(t *testing.T) {
	got := CanonicalEntityID("PERSON", "Donald Trump's")
	if got != "person:donald-trump" {
		t.Fatalf("possessive PERSON id = %q, want person:donald-trump", got)
	}
	if got := CanonicalEntityID("PERSON", "Donald Trump"); got != "person:donald-trump" {
		t.Fatalf("base PERSON id = %q, want person:donald-trump", got)
	}
}
