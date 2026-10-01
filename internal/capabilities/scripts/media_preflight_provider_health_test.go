package scriptgeneration

import (
	"context"
	"errors"
	"testing"
)

type stubImageProviderHealthProbe struct {
	err   error
	calls int
}

func (p *stubImageProviderHealthProbe) ProbeImageProviderHealth(context.Context) error {
	p.calls++
	return p.err
}

// TestRunMediaPreflight_ImageProviderHealthFailClosed certifies that a run
// whose image providers are all degraded fails the media preflight BEFORE any
// generation work, instead of discovering the failure after burning a full
// durable run.
func TestRunMediaPreflight_ImageProviderHealthFailClosed(t *testing.T) {
	probe := &stubImageProviderHealthProbe{err: errors.New("all retrieval providers down")}
	result := RunMediaPreflight(context.Background(), MediaPreflightInput{ImageProviderHealth: probe})
	if !result.HasFailures() {
		t.Fatal("RunMediaPreflight = no failures, want an image_providers failure")
	}
	found := false
	for _, failure := range result.Failures {
		if failure.Category == "image_providers" {
			found = true
		}
	}
	if !found {
		t.Fatalf("failures = %+v, want an image_providers entry", result.Failures)
	}
	if probe.calls != 1 {
		t.Fatalf("probe calls = %d, want 1", probe.calls)
	}
}

// TestRunMediaPreflight_ImageProviderHealthPasses certifies a healthy provider
// set is not a failure.
func TestRunMediaPreflight_ImageProviderHealthPasses(t *testing.T) {
	probe := &stubImageProviderHealthProbe{}
	result := RunMediaPreflight(context.Background(), MediaPreflightInput{ImageProviderHealth: probe})
	if result.HasFailures() {
		t.Fatalf("RunMediaPreflight = %+v, want no failures", result.Failures)
	}
	if probe.calls != 1 {
		t.Fatalf("probe calls = %d, want 1", probe.calls)
	}
}

// TestRunMediaPreflight_NoImageProviderProbeIsNoop certifies a run that does
// not depend on image retrieval (nil probe) is unaffected.
func TestRunMediaPreflight_NoImageProviderProbeIsNoop(t *testing.T) {
	result := RunMediaPreflight(context.Background(), MediaPreflightInput{})
	if result.HasFailures() {
		t.Fatalf("RunMediaPreflight = %+v, want no failures", result.Failures)
	}
}
