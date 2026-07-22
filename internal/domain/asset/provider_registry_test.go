package asset

import (
	"testing"
)

func TestProviderRegistry_RegisterAndByID(t *testing.T) {
	reg := NewProviderRegistry()
	if !reg.Register(ProviderDescriptor{ID: ProviderWikipedia}) {
		t.Fatal("expected Register to succeed")
	}
	if reg.Register(ProviderDescriptor{ID: ProviderWikipedia}) {
		t.Fatal("expected duplicate registration to fail")
	}

	d := reg.ByID(ProviderWikipedia)
	if d == nil {
		t.Fatal("expected descriptor by ID")
	}
	if d.ID != ProviderWikipedia {
		t.Errorf("ID = %q, want %q", d.ID, ProviderWikipedia)
	}
}

func TestProviderRegistry_MatchByAlias(t *testing.T) {
	reg := NewProviderRegistry()
	reg.Register(ProviderDescriptor{
		ID:      ProviderWikipedia,
		Aliases: []string{"wikipedia.org"},
		Origin:  ImageOriginRetrieved,
	})

	cases := map[string]ImageProvider{
		"https://en.wikipedia.org/wiki/Cat": ProviderWikipedia,
		"wikipedia":                         ProviderWikipedia,
		"duckduckgo":                        ProviderUnknown,
	}
	for source, want := range cases {
		d := reg.Match(source)
		var got ImageProvider
		if d != nil {
			got = d.ID
		} else {
			got = ProviderUnknown
		}
		if got != want {
			t.Errorf("Match(%q) = %q, want %q", source, got, want)
		}
	}
}

func TestDefaultProviderRegistry_ContainsKnownProviders(t *testing.T) {
	reg := DefaultProviderRegistry()

	for _, id := range []ImageProvider{ProviderWikipedia, ProviderDuckDuckGo, ProviderSearXNG, ProviderGoogleSlides, ProviderNVIDIA, ProviderFlux, ProviderUpload} {
		if reg.ByID(id) == nil {
			t.Errorf("default registry missing %q", id)
		}
	}
}

func TestDefaultProviderRegistry_ExactMatchAvoidsFalsePositives(t *testing.T) {
	reg := DefaultProviderRegistry()

	cases := []string{
		"https://uploader.example.com/path",
		"mydrive",
		"https://drive.google.com/file/d/abc123/view",
	}
	for _, source := range cases {
		if d := reg.Match(source); d != nil && (d.ID == ProviderUpload || d.ID == ProviderDrive) {
			t.Errorf("Match(%q) returned %q, expected no match for upload/drive", source, d.ID)
		}
	}
}

func TestDefaultProviderRegistry_ClassifiesOrigins(t *testing.T) {
	reg := DefaultProviderRegistry()

	tests := []struct {
		source string
		origin ImageOrigin
	}{
		{"wikipedia", ImageOriginRetrieved},
		{"https://en.wikipedia.org/wiki/Cat", ImageOriginRetrieved},
		{"google-slides", ImageOriginGenerated},
		{"flux-2-klein", ImageOriginGenerated},
		{"upload", ImageOriginUploaded},
	}
	for _, tt := range tests {
		d := reg.Match(tt.source)
		if d == nil {
			t.Errorf("Match(%q) returned nil", tt.source)
			continue
		}
		if d.Origin != tt.origin {
			t.Errorf("Match(%q).Origin = %q, want %q", tt.source, d.Origin, tt.origin)
		}
	}
}
