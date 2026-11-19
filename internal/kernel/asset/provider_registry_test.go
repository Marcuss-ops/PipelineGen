package asset

import (
	"errors"
	"testing"
)

func TestProviderRegistry_RegisterEmptyID(t *testing.T) {
	reg := NewProviderRegistry()
	err := reg.Register(ProviderDescriptor{})
	if !errors.Is(err, ErrProviderIDEmpty) {
		t.Fatalf("expected empty ID registration to fail with ErrProviderIDEmpty, got: %v", err)
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
