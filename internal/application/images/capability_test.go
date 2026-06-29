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
		CapImageGenChrome,
	} {
		if got := s.CapabilityResolution(cap); got != StatusNotImplemented {
			t.Errorf("nil receiver for %s: got %s, want %s", cap, got, StatusNotImplemented)
		}
	}
}

// TestCapabilityResolution_TruthfulMapping: A zero-value Service (no
// NVIDIA key, no remote URL) must report MissingDependency for the
// Chrome generator (not wired) and NotImplemented for the others.
func TestCapabilityResolution_TruthfulMapping(t *testing.T) {
	s := &Service{} // zero-valued

	want := map[Capability]CapabilityStatus{
		CapImageGenNvidia: StatusNotImplemented,    // Disabled
		CapRemoteImageGen: StatusNotImplemented,    // Disabled
		CapImageGenChrome: StatusMissingDependency, // imageGen not wired
	}
	for cap, want := range want {
		if got := s.CapabilityResolution(cap); got != want {
			t.Errorf("capability %s: got %s, want %s", cap, got, want)
		}
	}
}

// TestCapabilityResolution_NvidiaKeyAvailable: Even setting nvidiaAPIKey on
// the Service, CapImageGenNvidia remains StatusNotImplemented since Chrome/Slides is the only provider.
func TestCapabilityResolution_NvidiaKeyAvailable(t *testing.T) {
	s := &Service{nvidiaAPIKey: "nvapi-real-key-12345"}

	if got := s.CapabilityResolution(CapImageGenNvidia); got != StatusNotImplemented {
		t.Errorf("CapImageGenNvidia with real key: got %s, want %s", got, StatusNotImplemented)
	}
	// Remote still not implemented.
	if got := s.CapabilityResolution(CapRemoteImageGen); got != StatusNotImplemented {
		t.Errorf("CapRemoteImageGen: got %s, want %s", got, StatusNotImplemented)
	}
	// Chrome still missing (not wired).
	if got := s.CapabilityResolution(CapImageGenChrome); got != StatusMissingDependency {
		t.Errorf("CapImageGenChrome: got %s, want %s", got, StatusMissingDependency)
	}
}

// TestCapabilityResolution_NvidiaKeyPlaceholder: Checked for completeness.
func TestCapabilityResolution_NvidiaKeyPlaceholder(t *testing.T) {
	s := &Service{nvidiaAPIKey: nvidiaAPIKeyPlaceholder}
	if got := s.CapabilityResolution(CapImageGenNvidia); got != StatusNotImplemented {
		t.Errorf("CapImageGenNvidia with placeholder: got %s, want %s", got, StatusNotImplemented)
	}
}

// TestCapabilityResolution_ChromeAvailable: Wiring an ImageGenerator
// flips CapImageGenChrome to StatusAvailable. Uses a non-nil dummy to
// satisfy the ImageGenerator interface.
func TestCapabilityResolution_ChromeAvailable(t *testing.T) {
	s := &Service{imageGen: &ChromeImageProvider{}}

	if got := s.CapabilityResolution(CapImageGenChrome); got != StatusAvailable {
		t.Errorf("CapImageGenChrome with wired provider: got %s, want %s", got, StatusAvailable)
	}
	// Other capabilities unchanged.
	if got := s.CapabilityResolution(CapImageGenNvidia); got != StatusNotImplemented {
		t.Errorf("CapImageGenNvidia: got %s, want %s", got, StatusNotImplemented)
	}
}
// remoteImageEndpointURL on the Service, CapRemoteImageGen remains
// StatusNotImplemented since Chrome/Slides is the only provider.
func TestCapabilityResolution_RemoteURLAvailable(t *testing.T) {
	s := &Service{remoteImageEndpointURL: "https://google-flow.example.com/v1/generate"}

	if got := s.CapabilityResolution(CapRemoteImageGen); got != StatusNotImplemented {
		t.Errorf("CapRemoteImageGen with URL: got %s, want %s", got, StatusNotImplemented)
	}
	if got := s.CapabilityResolution(CapImageGenNvidia); got != StatusNotImplemented {
		t.Errorf("CapImageGenNvidia: got %s, want %s", got, StatusNotImplemented)
	}
}

// TestAllCapabilities_ContainsAllCapabilities: The AllCapabilities
// map must contain EVERY Capability constant (not just a subset) so
// the diagnostic endpoint + future consumers can iterate over it
// without holding a parallel list.
func TestAllCapabilities_ContainsAllCapabilities(t *testing.T) {
	s := &Service{} // zero-valued
	all := s.AllCapabilities()

	for _, cap := range []Capability{
		CapImageGenNvidia,
		CapRemoteImageGen,
		CapImageGenChrome,
	} {
		if _, ok := all[cap]; !ok {
			t.Errorf("AllCapabilities missing entry for %s", cap)
		}
	}
	if len(all) != 3 {
		t.Errorf("AllCapabilities length = %d, want 3 (one per known Capability)", len(all))
	}
}
