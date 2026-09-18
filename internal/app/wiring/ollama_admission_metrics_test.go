package wiring

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
)

// TestOllamaAdmissionObserverPublishesCollectors pins the bridge: the adapter
// must move the limiter's samples into the canonical collectors owned by
// platform/observability, with the endpoint as the only label.
func TestOllamaAdmissionObserverPublishesCollectors(t *testing.T) {
	const endpoint = "http://127.0.0.1:11434/test-wiring-adapter"

	observer := ollamaAdmissionObserver{}
	observer.AdmissionChanged(endpoint, 3, 2)

	if got := testutil.ToFloat64(observability.OllamaAdmissionLimit.WithLabelValues(endpoint)); got != 3 {
		t.Fatalf("limit gauge = %v, want 3", got)
	}
	if got := testutil.ToFloat64(observability.OllamaAdmissionInFlight.WithLabelValues(endpoint)); got != 2 {
		t.Fatalf("in-flight gauge = %v, want 2", got)
	}

	observer.AdmissionDeferred(endpoint)
	observer.AdmissionDeferred(endpoint)
	if got := testutil.ToFloat64(observability.OllamaAdmissionDeferredTotal.WithLabelValues(endpoint)); got != 2 {
		t.Fatalf("deferred counter = %v, want 2", got)
	}

	// Draining must be visible: the gauge tracks down as well as up.
	observer.AdmissionChanged(endpoint, 3, 0)
	if got := testutil.ToFloat64(observability.OllamaAdmissionInFlight.WithLabelValues(endpoint)); got != 0 {
		t.Fatalf("in-flight gauge after drain = %v, want 0", got)
	}
}

// TestObserveOllamaAdmissionIsNilSafe keeps the composition path tolerant: a
// missing client must not panic (telemetry is optional, the budget is not).
func TestObserveOllamaAdmissionIsNilSafe(t *testing.T) {
	observeOllamaAdmission(nil)
}
