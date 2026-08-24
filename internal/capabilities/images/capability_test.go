package images

import (
	"context"
	"testing"
)

type capabilityImageGeneratorFake struct{}

func (capabilityImageGeneratorFake) Generate(context.Context, GenerateImageRequest) (*GeneratedImage, error) {
	return nil, ErrImageGenProviderNotAvailable
}

func (capabilityImageGeneratorFake) TriggerPrewarm(context.Context, string, int) {}

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
	s := &Service{Diag: &DiagnosticsService{imageGen: capabilityImageGeneratorFake{}}}
	if got := s.CapabilityResolution(CapImageGenChrome); got != StatusAvailable {
		t.Errorf("Google Slides capability: got %s, want %s", got, StatusAvailable)
	}
}

func TestCapabilityResolution_UnknownCapabilityNotImplemented(t *testing.T) {
	s := &Service{Diag: &DiagnosticsService{imageGen: capabilityImageGeneratorFake{}}}
	if got := s.CapabilityResolution(Capability("removed-provider")); got != StatusNotImplemented {
		t.Errorf("unknown capability: got %s, want %s", got, StatusNotImplemented)
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
}
