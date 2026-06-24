package images

import "testing"

// TestCapabilityResolution_NilSafe: a nil *Service must NOT panic and
// must report NotImplemented for any capability. Handlers in tests +
// partial wiring call into Service{} values before the composition
// root has populated the fields; the resolver must degrade safely.
func TestCapabilityResolution_NilSafe(t *testing.T) {
	var s *Service // nil receiver
	for _, cap := range []Capability{
		CapImageGenNvidia,
		CapRemoteImageGen,
		CapGoogleSlidesImage,
		CapVideoAI,
		CapPrewarm,
	} {
		if got := s.CapabilityResolution(cap); got != StatusNotImplemented {
			t.Errorf("nil receiver for %s: got %s, want %s", cap, got, StatusNotImplemented)
		}
	}
}

// TestCapabilityResolution_TruthfulMapping: A zero-value Service (no
// NVIDIA key, no remote URL) must report MissingDependency for the
// configurable deps and NotImplemented for the stubs. This is the
// core "truthful" claim of fix(images): expose truthful capability
// availability — handlers + the diagnostic endpoint must see
// distinct statuses for these 5 capabilities.
func TestCapabilityResolution_TruthfulMapping(t *testing.T) {
	s := &Service{} // zero-valued: nvidiaAPIKey == "", remoteImageEndpointURL == ""

	want := map[Capability]CapabilityStatus{
		CapImageGenNvidia:    StatusMissingDependency, // NVIDIA_API_KEY not set
		CapRemoteImageGen:    StatusMissingDependency, // VELOX_REMOTE_IMAGE_ENDPOINT not set
		CapGoogleSlidesImage: StatusNotImplemented,    // stub (track: image-google-slides)
		CapVideoAI:           StatusNotImplemented,    // stub (GenerateVideoAI is a no-op error)
		CapPrewarm:           StatusNotImplemented,    // stub (TriggerPrewarm is a debug-log no-op)
	}
	for cap, want := range want {
		if got := s.CapabilityResolution(cap); got != want {
			t.Errorf("capability %s: got %s, want %s", cap, got, want)
		}
	}
}

// TestCapabilityResolution_NvidiaKeyAvailable: Setting nvidiaAPIKey on
// the Service flips CapImageGenNvidia to StatusAvailable; other
// capabilities retain their wiring.
func TestCapabilityResolution_NvidiaKeyAvailable(t *testing.T) {
	s := &Service{nvidiaAPIKey: "nvapi-real-key-12345"}

	if got := s.CapabilityResolution(CapImageGenNvidia); got != StatusAvailable {
		t.Errorf("CapImageGenNvidia with real key: got %s, want %s", got, StatusAvailable)
	}
	// Remote still missing.
	if got := s.CapabilityResolution(CapRemoteImageGen); got != StatusMissingDependency {
		t.Errorf("CapRemoteImageGen: got %s, want %s", got, StatusMissingDependency)
	}
	// Stubs unchanged.
	for _, cap := range []Capability{CapGoogleSlidesImage, CapVideoAI, CapPrewarm} {
		if got := s.CapabilityResolution(cap); got != StatusNotImplemented {
			t.Errorf("capability %s: got %s, want %s (stubs unchanged by NVIDIA key)", cap, got, StatusNotImplemented)
		}
	}
}

// TestCapabilityResolution_NvidiaKeyPlaceholder: The well-known "not
// set" placeholder string is treated as equivalent to empty — devs
// without a real key must NOT see Available.
func TestCapabilityResolution_NvidiaKeyPlaceholder(t *testing.T) {
	s := &Service{nvidiaAPIKey: nvidiaAPIKeyPlaceholder}
	if got := s.CapabilityResolution(CapImageGenNvidia); got != StatusMissingDependency {
		t.Errorf("CapImageGenNvidia with placeholder: got %s, want %s", got, StatusMissingDependency)
	}
}

// TestCapabilityResolution_RemoteURLAvailable: Setting
// remoteImageEndpointURL on the Service flips CapRemoteImageGen to
// StatusAvailable; other capabilities retain their wiring.
func TestCapabilityResolution_RemoteURLAvailable(t *testing.T) {
	s := &Service{remoteImageEndpointURL: "https://google-flow.example.com/v1/generate"}

	if got := s.CapabilityResolution(CapRemoteImageGen); got != StatusAvailable {
		t.Errorf("CapRemoteImageGen with URL: got %s, want %s", got, StatusAvailable)
	}
	if got := s.CapabilityResolution(CapImageGenNvidia); got != StatusMissingDependency {
		t.Errorf("CapImageGenNvidia: got %s, want %s", got, StatusMissingDependency)
	}
}

// TestAllCapabilities_ContainsAllFiveCapabilities: The AllCapabilities
// map must contain EVERY Capability constant (not just a subset) so
// the diagnostic endpoint + future consumers can iterate over it
// without holding a parallel list.
func TestAllCapabilities_ContainsAllFiveCapabilities(t *testing.T) {
	s := &Service{} // zero-valued
	all := s.AllCapabilities()

	for _, cap := range []Capability{
		CapImageGenNvidia,
		CapRemoteImageGen,
		CapGoogleSlidesImage,
		CapVideoAI,
		CapPrewarm,
	} {
		if _, ok := all[cap]; !ok {
			t.Errorf("AllCapabilities missing entry for %s", cap)
		}
	}
	if len(all) != 5 {
		t.Errorf("AllCapabilities length = %d, want 5 (one per known Capability)", len(all))
	}
}
