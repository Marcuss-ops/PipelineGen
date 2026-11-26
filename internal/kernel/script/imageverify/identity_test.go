package imageverify

import "testing"

// TestPersonImageFilenameQualifiersAreEvidenceBased pins the qualifier
// vocabulary. The rule is owned here (PersonImageSourceIdentityFailure); VidRush
// used to carry a byte-identical copy of the predicate, so a change here is the
// only way the vocabulary can move.
func TestPersonImageFilenameQualifiersAreEvidenceBased(t *testing.T) {
	for _, token := range []string{"foto", "photo", "portrait", "headshot", "image", "img", "profile", "ritratto"} {
		if !personImageFilenameQualifier(token) {
			t.Errorf("expected %q to be a known filename qualifier", token)
		}
	}
	for _, token := range []string{"avvocato", "avvocata", "ansa", "ipa"} {
		if personImageFilenameQualifier(token) {
			t.Errorf("%q is a broad editorial word, not a filename qualifier", token)
		}
	}
}

// TestPersonImageSourceIdentityFailureIsDecisive pins the two decisions the
// shared rule makes: a filename that names the person with a different surname
// is a conflict, a generic filename is not.
func TestPersonImageSourceIdentityFailureIsDecisive(t *testing.T) {
	if got := PersonImageSourceIdentityFailure("Angela Taccia", "https://images.example/angela-tuccia.jpg", ""); got == "" {
		t.Fatal("a different surname must be a provenance conflict")
	}
	for _, generic := range []string{
		"https://images.example/IMG_6075.jpg",
		"https://images.example/wp-content/uploads/2026/05/portrait.jpg",
	} {
		if got := PersonImageSourceIdentityFailure("Angela Taccia", generic, ""); got != "" {
			t.Errorf("generic filename %q must stay eligible: %s", generic, got)
		}
	}
}
