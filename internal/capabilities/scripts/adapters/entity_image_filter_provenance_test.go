package adapters

import "testing"

func TestPersonImageSourceNameMismatchRejectsMultiNameFilename(t *testing.T) {
	got := personImageSourceNameMismatch("John Gotti", "https://images.example/john-gotti-sammy-gravano.jpg")
	if got == "" {
		t.Fatal("a filename naming a second person must not pass as John Gotti")
	}
}

func TestPersonImageSourceNameMismatchAllowsCanonicalQualifiedFilename(t *testing.T) {
	for _, source := range []string{
		"https://images.example/john-gotti-photo-2020.jpg",
		"https://images.example/john-gotti-1990.jpg",
		"https://images.example/john-gotti-portrait.jpg",
	} {
		if got := personImageSourceNameMismatch("John Gotti", source); got != "" {
			t.Fatalf("canonical filename %q rejected: %s", source, got)
		}
	}
	if got := personImageSourceNameMismatch("José María Aznar", "https://www.21news.be/wp-content/uploads/2026/02/Jose-Maria-Aznar-scaled.jpg"); got != "" {
		t.Fatalf("compound surname filename rejected: %s", got)
	}
	if got := personImageSourceNameMismatch("Conan O'Brien", "https://www.peopleandmedia.com/wp-content/uploads/2026/05/conan-obrien-portrait.jpg"); got != "" {
		t.Fatalf("apostrophe surname filename rejected: %s", got)
	}
}

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
