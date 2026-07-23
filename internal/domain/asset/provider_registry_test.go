package asset

import (
	"context"
	"errors"
	"testing"
)

func TestProviderRegistry_RegisterAndByID(t *testing.T) {
	reg := NewProviderRegistry()
	if err := reg.Register(ProviderDescriptor{ID: ProviderWikipedia}); err != nil {
		t.Fatalf("expected Register to succeed: %v", err)
	}
	if err := reg.Register(ProviderDescriptor{ID: ProviderWikipedia}); err == nil {
		t.Fatal("expected duplicate registration to fail")
	}

	d, ok := reg.ByID(ProviderWikipedia)
	if !ok {
		t.Fatal("expected descriptor by ID")
	}
	if d.ID != ProviderWikipedia {
		t.Errorf("ID = %q, want %q", d.ID, ProviderWikipedia)
	}
}

func TestProviderRegistry_RegisterEmptyID(t *testing.T) {
	reg := NewProviderRegistry()
	err := reg.Register(ProviderDescriptor{})
	if !errors.Is(err, ErrProviderIDEmpty) {
		t.Fatalf("expected empty ID registration to fail with ErrProviderIDEmpty, got: %v", err)
	}
}

func TestProviderRegistry_Seal(t *testing.T) {
	reg := NewProviderRegistry()
	if err := reg.Register(ProviderDescriptor{ID: ProviderWikipedia}); err != nil {
		t.Fatalf("register before seal: %v", err)
	}
	reg.Seal()
	if err := reg.Register(ProviderDescriptor{ID: ProviderDuckDuckGo}); !errors.Is(err, ErrProviderRegistrySealed) {
		t.Fatalf("expected sealed registry to reject Register, got: %v", err)
	}
	// Reads on a sealed registry still work.
	if _, ok := reg.ByID(ProviderWikipedia); !ok {
		t.Fatal("expected ByID to work after seal")
	}
}

func TestProviderRegistry_MatchByAlias(t *testing.T) {
	reg := NewProviderRegistry()
	if err := reg.Register(ProviderDescriptor{
		ID:      ProviderWikipedia,
		Aliases: []string{"wikipedia.org"},
		Origin:  ImageOriginRetrieved,
	}); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	cases := map[string]ImageProvider{
		"https://en.wikipedia.org/wiki/Cat": ProviderWikipedia,
		"wikipedia":                         ProviderWikipedia,
		"duckduckgo":                        ProviderUnknown,
	}
	for source, want := range cases {
		d, ok := reg.Match(source)
		var got ImageProvider
		if ok {
			got = d.ID
		} else {
			got = ProviderUnknown
		}
		if got != want {
			t.Errorf("Match(%q) = %q, want %q", source, got, want)
		}
	}
}

func TestProviderRegistry_MatchExactAvoidsFalsePositives(t *testing.T) {
	reg := NewProviderRegistry()
	if err := reg.Register(ProviderDescriptor{
		ID:      ProviderUpload,
		Aliases: []string{"upload"},
		Origin:  ImageOriginUploaded,
	}); err != nil {
		t.Fatalf("Register upload failed: %v", err)
	}
	if err := reg.Register(ProviderDescriptor{
		ID:      ProviderDrive,
		Aliases: []string{"drive", "drive.google.com"},
		Origin:  ImageOriginRetrieved,
	}); err != nil {
		t.Fatalf("Register drive failed: %v", err)
	}

	// These should not match because they are not exact aliases/IDs or hostnames.
	noMatch := []string{
		"uploader",
		"mydrive",
		"uploaded file",
	}
	for _, source := range noMatch {
		if _, ok := reg.Match(source); ok {
			t.Errorf("Match(%q) unexpectedly matched", source)
		}
	}

	// These should match because they are exact aliases/IDs or hostnames.
	matchCases := map[string]ImageProvider{
		"upload": ProviderUpload,
		"drive":  ProviderDrive,
		"https://drive.google.com/file/d/abc123/view": ProviderDrive,
	}
	for source, want := range matchCases {
		d, ok := reg.Match(source)
		if !ok {
			t.Fatalf("Match(%q) returned no match, want %q", source, want)
		}
		if d.ID != want {
			t.Errorf("Match(%q) = %q, want %q", source, d.ID, want)
		}
	}
}

func TestProviderRegistry_AllDefensiveCopy(t *testing.T) {
	reg := NewProviderRegistry()
	if err := reg.Register(ProviderDescriptor{
		ID:      ProviderWikipedia,
		Aliases: []string{"wikipedia"},
		Origin:  ImageOriginRetrieved,
	}); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	all := reg.All()
	if len(all) != 1 {
		t.Fatalf("len(All()) = %d, want 1", len(all))
	}
	all[0].ID = ProviderDuckDuckGo

	d, ok := reg.ByID(ProviderWikipedia)
	if !ok {
		t.Fatal("mutating All() slice should not affect internal registry")
	}
	if d.ID != ProviderWikipedia {
		t.Errorf("internal descriptor ID = %q after mutating All() copy", d.ID)
	}
}

func TestDefaultProviderRegistry_ContainsKnownProviders(t *testing.T) {
	reg := DefaultProviderRegistry()

	for _, id := range []ImageProvider{ProviderWikipedia, ProviderDuckDuckGo, ProviderSearXNG, ProviderGoogleSlides, ProviderNVIDIA, ProviderFlux, ProviderUpload} {
		if _, ok := reg.ByID(id); !ok {
			t.Errorf("default registry missing %q", id)
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
		d, ok := reg.Match(tt.source)
		if !ok {
			t.Errorf("Match(%q) returned no match", tt.source)
			continue
		}
		if d.Origin != tt.origin {
			t.Errorf("Match(%q).Origin = %q, want %q", tt.source, d.Origin, tt.origin)
		}
	}
}

func TestDefaultProviderRegistry_LicenseResolver(t *testing.T) {
	reg := DefaultProviderRegistry()

	cases := []struct {
		id   ImageProvider
		want string
	}{
		{ProviderWikipedia, "CC-BY-SA-4.0"},
		{ProviderDuckDuckGo, "unknown"},
		{ProviderGoogleSlides, "proprietary"},
	}
	for _, tt := range cases {
		d, ok := reg.ByID(tt.id)
		if !ok {
			t.Fatalf("ByID(%q) not found", tt.id)
		}
		if d.LicenseResolver == nil {
			t.Fatalf("LicenseResolver for %q is nil", tt.id)
		}
		got, err := d.LicenseResolver(context.Background(), nil)
		if err != nil {
			t.Fatalf("LicenseResolver(%q) error: %v", tt.id, err)
		}
		if got != tt.want {
			t.Errorf("LicenseResolver(%q) = %q, want %q", tt.id, got, tt.want)
		}
	}
}

func TestDefaultProviderRegistry_MetadataMapper(t *testing.T) {
	reg := DefaultProviderRegistry()

	d, ok := reg.ByID(ProviderDuckDuckGo)
	if !ok {
		t.Fatal("DuckDuckGo descriptor not found")
	}
	if d.MetadataMapper == nil {
		t.Fatal("MetadataMapper is nil")
	}
	out, err := d.MetadataMapper(Metadata{"foo": "bar"})
	if err != nil {
		t.Fatalf("MetadataMapper error: %v", err)
	}
	if out["provider"] != string(ProviderDuckDuckGo) {
		t.Errorf("provider = %q, want %q", out["provider"], ProviderDuckDuckGo)
	}
	if out["origin"] != string(ImageOriginRetrieved) {
		t.Errorf("origin = %q, want %q", out["origin"], ImageOriginRetrieved)
	}
	if out["foo"] != "bar" {
		t.Errorf("existing key foo = %q, want bar", out["foo"])
	}
}
