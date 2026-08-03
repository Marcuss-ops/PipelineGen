package script

import (
	"errors"
	"testing"
)

func TestSanitizeNarrationExtractsSourcesAndLeavesSpeakableText(t *testing.T) {
	clean, sources, err := SanitizeNarration("Il punto di svolta arrivò nel 2003. [Fonte: Bancarotta di Tyson](https://example.com/tyson) www.example.org/report")
	if err != nil {
		t.Fatalf("SanitizeNarration() error = %v", err)
	}
	if clean != "Il punto di svolta arrivò nel 2003." {
		t.Fatalf("clean text = %q", clean)
	}
	if len(sources) != 2 || sources[0].Title != "Bancarotta di Tyson" || sources[1].URL != "www.example.org/report" {
		t.Fatalf("sources = %#v", sources)
	}
	if err := ValidateSpeakableText(clean); err != nil {
		t.Fatalf("clean text rejected: %v", err)
	}
}

func TestValidateSpeakableTextFailsClosed(t *testing.T) {
	err := ValidateSpeakableText("Narrazione [Fonte: articolo](https://example.com)")
	if !errors.Is(err, ErrNarrationNotSpeakable) {
		t.Fatalf("error = %v, want ErrNarrationNotSpeakable", err)
	}
}
