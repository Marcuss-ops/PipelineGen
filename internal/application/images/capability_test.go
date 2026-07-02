package images

import "testing"

func TestCapabilityResolution_NilSafe(t *testing.T) {
	var s *Service
	if got := s.CapabilityResolution(CapImageGenChrome); got != StatusNotImplemented {
		t.Errorf("nil receiver: got %s, want %s", got, StatusNotImplemented)
	}
}

func TestCapabilityResolution_MissingGoogleSlidesDependency(t *testing.T) {
	s := &Service{Diag: &DiagnosticsService{}}
	if got := s.CapabilityResolution(CapImageGenChrome); got != StatusMissingDependency {
		t.Errorf("Google Slides capability: got %s, want %s", got, StatusMissingDependency)
	}
}

func TestCapabilityResolution_GoogleSlidesAvailable(t *testing.T) {
	s := &Service{Diag: &DiagnosticsService{imageGen: &ChromeImageProvider{}}}
	if got := s.CapabilityResolution(CapImageGenChrome); got != StatusAvailable {
		t.Errorf("Google Slides capability: got %s, want %s", got, StatusAvailable)
	}
}

func TestCapabilityResolution_DeprecatedProviderIDsNotImplemented(t *testing.T) {
	s := &Service{Diag: &DiagnosticsService{
		imageGen:     &ChromeImageProvider{},
		nvidiaAPIKey: "legacy-key-must-not-enable-provider",
	}}
	for _, cap := range []Capability{CapImageGenNvidia, CapRemoteImageGen} {
		if got := s.CapabilityResolution(cap); got != StatusNotImplemented {
			t.Errorf("deprecated capability %s: got %s, want %s", cap, got, StatusNotImplemented)
		}
	}
}

func TestAllCapabilities_AdvertisesOnlyGoogleSlides(t *testing.T) {
	s := &Service{Diag: &DiagnosticsService{}}
	all := s.AllCapabilities()
	if len(all) != 1 {
		t.Fatalf("AllCapabilities length = %d, want 1", len(all))
	}
	if got, ok := all[CapImageGenChrome]; !ok {
		t.Fatal("Google Slides capability missing")
	} else if got != StatusMissingDependency {
		t.Fatalf("Google Slides status = %s, want %s", got, StatusMissingDependency)
	}
	if _, ok := all[CapImageGenNvidia]; ok {
		t.Fatal("NVIDIA capability must not be advertised")
	}
	if _, ok := all[CapRemoteImageGen]; ok {
		t.Fatal("remote image generation capability must not be advertised")
	}
}
