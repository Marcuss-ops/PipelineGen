// Package scripts \u2014 clip_sampler_registry_test.go pins the
// FASE-7 registry contract: single-impl invariant (every caller
// tag resolves to the SAME impl instance) and nil-receiver
// fail-closed panic (godlike/07 NO-FAKE-AVAILABILITY).
package usecase

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
)

func TestSamplerRegistry_SingleImplInvariantPerCallerTag(t *testing.T) {
	// godlike/06 SSOT: there is exactly ONE sampler impl instance
	// regardless of how many callers ask for it. Distinct SamplerFor
	// calls with different caller tags MUST return the SAME
	// pointer \u2014 confirmed via pointer-equality below.
	reg := NewClipSamplerRegistry()
	a := reg.SamplerFor(ClipSamplerCallerSearch)
	b := reg.SamplerFor(ClipSamplerCallerCatalog)
	c := reg.SamplerFor(ClipSamplerCallerCurate)
	if a == nil || b == nil || c == nil {
		t.Fatal("registry returned nil sampler for any caller tag")
	}
	if a != b || b != c {
		t.Errorf("expected single-impl invariant (pointer-equal across caller tags); got distinct: %p %p %p", a, b, c)
	}
	// Type assertion: the impl must satisfy the canonical port.
	var _ ports.ClipSampler = a
}

func TestSamplerRegistry_NilReceiverPanicsFailClosed(t *testing.T) {
	// godlike/07 NO-FAKE-AVAILABILITY: a nil registry must panic
	// loudly so wiring bugs surface at first use instead of
	// producing a silent degraded selection.
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on nil receiver (godlike/07 fail-closed)")
		}
	}()
	var reg *ClipSamplerRegistry
	_ = reg.SamplerFor(ClipSamplerCallerSearch)
}
