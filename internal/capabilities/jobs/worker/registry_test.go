// Package worker — AZIONE 7 (July 2026): ProducesArtifacts unit tests.
//
// Pins the SetProducesArtifacts → ProducesArtifacts round-trip and the nil-safe
// boundary contracts (nil receiver, unknown job type, overwrite).
package worker

import (
	"testing"
)

// TestRegistry_ProducesArtifacts_TrueAfterSet pins the canonical
// AZIONE 7 contract: SetProducesArtifacts(jobType, true) → ProducesArtifacts
// returns true for the same jobType.
func TestRegistry_ProducesArtifacts_TrueAfterSet(t *testing.T) {
	reg := NewRegistry()
	reg.SetProducesArtifacts("script.generate", true)

	if !reg.ProducesArtifacts("script.generate") {
		t.Error("ProducesArtifacts(script.generate) = false after SetProducesArtifacts(true), want true")
	}
}

// TestRegistry_ProducesArtifacts_FalseByDefault pins the zero-value
// contract: a never-set job type returns false (the default).
func TestRegistry_ProducesArtifacts_FalseByDefault(t *testing.T) {
	reg := NewRegistry()

	if reg.ProducesArtifacts("unknown.job.type") {
		t.Error("ProducesArtifacts(unknown) = true for never-set type, want false (zero-value default)")
	}
}

// TestRegistry_ProducesArtifacts_ExplicitFalseOverwrites pins the
// overwrite contract: SetProducesArtifacts(jobType, false) after a prior
// Set(true) must return false.
func TestRegistry_ProducesArtifacts_ExplicitFalseOverwrites(t *testing.T) {
	reg := NewRegistry()
	reg.SetProducesArtifacts("script.generate", true)
	reg.SetProducesArtifacts("script.generate", false)

	if reg.ProducesArtifacts("script.generate") {
		t.Error("ProducesArtifacts(script.generate) = true after Set(false), want false (overwrite honoured)")
	}
}

// TestRegistry_ProducesArtifacts_NilReceiver pins the nil-safe contract:
// ProducesArtifacts on a nil *Registry must return false without panicking.
func TestRegistry_ProducesArtifacts_NilReceiver(t *testing.T) {
	var reg *Registry
	if reg.ProducesArtifacts("any.type") {
		t.Error("ProducesArtifacts on nil receiver returned true, want false (nil-safe)")
	}
}

// TestRegistry_SetProducesArtifacts_NilReceiver pins the nil-safe contract
// for SetProducesArtifacts: calling it on a nil receiver is a no-op (no panic).
func TestRegistry_SetProducesArtifacts_NilReceiver(t *testing.T) {
	var reg *Registry
	result := reg.SetProducesArtifacts("any.type", true)
	if result != nil {
		t.Error("SetProducesArtifacts on nil receiver returned non-nil, want nil (fluent nil-safe no-op)")
	}
}

// TestRegistry_SetProducesArtifacts_FluentChain pins the fluent builder
// contract: SetProducesArtifacts returns the receiver for chaining.
func TestRegistry_SetProducesArtifacts_FluentChain(t *testing.T) {
	reg := NewRegistry()
	result := reg.SetProducesArtifacts("a", true)
	if result != reg {
		t.Error("SetProducesArtifacts must return the receiver for fluent chaining")
	}
}

// TestRegistry_ProducesArtifacts_ConcurrentSafety exercises the RWMutex
// contract: concurrent reads of ProducesArtifacts must not race with a
// concurrent SetProducesArtifacts write.
func TestRegistry_ProducesArtifacts_ConcurrentSafety(t *testing.T) {
	reg := NewRegistry()
	reg.SetProducesArtifacts("concurrent.test", true)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			reg.SetProducesArtifacts("concurrent.test", true)
		}
	}()

	// Read concurrently while the writer goroutine is still active.
	for i := 0; i < 100; i++ {
		_ = reg.ProducesArtifacts("concurrent.test")
	}
	<-done
}
