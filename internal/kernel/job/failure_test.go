package job_test

import (
	"errors"
	"fmt"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// TestFailureClassRetryabilityIsExplicit pins the only two classes that must
// never be retried. Everything else keeps the historical behaviour, including
// FailureUnknown: an unclassified failure must not become permanent by
// accident.
func TestFailureClassRetryabilityIsExplicit(t *testing.T) {
	cases := []struct {
		class job.FailureClass
		want  bool
	}{
		{job.FailureDeterministic, false},
		{job.FailureConfiguration, false},
		{job.FailureProvider, true},
		{job.FailureDeadline, true},
		{job.FailureInfrastructure, true},
		{job.FailureUnknown, true},
	}
	for _, tc := range cases {
		if got := tc.class.Retryable(); got != tc.want {
			t.Errorf("%s.Retryable = %v, want %v", tc.class, got, tc.want)
		}
	}
}

// TestClassifiedFailureKeepsMessageAndAddsClass pins the persisted shape: the
// original message stays readable, and class/stage/code are greppable.
func TestClassifiedFailureKeepsMessageAndAddsClass(t *testing.T) {
	err := job.NewClassifiedFailure(job.FailureDeterministic, "PUBLISHING_DOCUMENTS", "PUBLISHING_DOCUMENTS_FAILED", "window does not fit")
	if got := err.Error(); got != "window does not fit (class=deterministic_contract stage=PUBLISHING_DOCUMENTS code=PUBLISHING_DOCUMENTS_FAILED)" {
		t.Fatalf("Error() = %q", got)
	}
	if !err.Permanent() {
		t.Fatal("deterministic contract failure must be permanent")
	}
	if job.NewClassifiedFailure(job.FailureProvider, "", "", "").Permanent() {
		t.Fatal("a provider failure must remain retryable")
	}
}

// TestClassifiedFailureIsExtractableThroughWrapping keeps the worker free to
// wrap the error without losing the class.
func TestClassifiedFailureIsExtractableThroughWrapping(t *testing.T) {
	base := job.NewClassifiedFailure(job.FailureConfiguration, "GENERATING_SCENE_TEXT", "TRANSLATION_FAILED", "no provider for it->de")
	wrapped := fmt.Errorf("handler: %w", base)
	got, ok := job.ClassifiedFailureFrom(wrapped)
	if !ok || got != base {
		t.Fatalf("ClassifiedFailureFrom = %v, %v; want the wrapped failure", got, ok)
	}
	if _, ok := job.ClassifiedFailureFrom(nil); ok {
		t.Fatal("nil must not be a classified failure")
	}
	if _, ok := job.ClassifiedFailureFrom(errors.New("plain")); ok {
		t.Fatal("a plain error must not be a classified failure")
	}
}

// TestClassifiedFailureIsNotRetryableByTheRetryGate is the guard that keeps the
// taxonomy from silently changing the retry gate: carrying a class must not make
// the error satisfy the typed transient probe.
func TestClassifiedFailureIsNotRetryableByTheRetryGate(t *testing.T) {
	err := job.NewClassifiedFailure(job.FailureProvider, "GENERATING_SCENE_TEXT", "PROVIDER_TIMEOUT", "timeout")
	var retryable interface{ IsRetryable() bool }
	if errors.As(err, &retryable) {
		t.Fatal("ClassifiedFailure must not implement IsRetryable: retry is gated by typed transience, not by class")
	}
}
