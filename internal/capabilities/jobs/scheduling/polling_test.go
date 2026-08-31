package scheduling

import (
	"testing"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

func TestPollingStateRecordEmptyEscalatesAfterThreshold(t *testing.T) {
	base := time.Second
	cfg := BackoffConfig{MaxBackoff: 8 * time.Second, ConsecutiveEmptyThreshold: 1}
	state := NewPollingState(base)

	var escalated bool
	state, escalated = state.RecordEmpty(base, cfg)
	if escalated || state.CurrentBackoff != base || state.ConsecutiveEmpty != 1 {
		t.Fatalf("first empty = %#v, escalated=%v", state, escalated)
	}
	state, escalated = state.RecordEmpty(base, cfg)
	if !escalated || state.CurrentBackoff != 2*time.Second || state.ConsecutiveEmpty != 2 {
		t.Fatalf("second empty = %#v, escalated=%v", state, escalated)
	}
	state, escalated = state.RecordEmpty(base, cfg)
	if !escalated || state.CurrentBackoff != 4*time.Second {
		t.Fatalf("third empty = %#v, escalated=%v", state, escalated)
	}
}

func TestPollingStateResetAfterClaim(t *testing.T) {
	state := PollingState{CurrentBackoff: 4 * time.Second, ConsecutiveEmpty: 3}
	got := state.ResetAfterClaim(time.Second)
	want := PollingState{CurrentBackoff: time.Second}
	if got != want {
		t.Fatalf("reset = %#v, want %#v", got, want)
	}
}

func TestRetryDecisionAndBackoff(t *testing.T) {
	if DecideRetry(nil) != RetryTerminal {
		t.Fatal("nil job must be terminal")
	}
	if DecideRetry(&job.Job{RetryCount: 2, MaxRetries: 2}) != RetryTerminal {
		t.Fatal("exhausted retry budget must be terminal")
	}
	if DecideRetry(&job.Job{RetryCount: 1, MaxRetries: 2}) != RetryScheduled {
		t.Fatal("available retry budget must be scheduled")
	}
	if got := RetryBackoff(2, DefaultRetryPolicy); got != 8*time.Second {
		t.Fatalf("retry backoff = %v, want 8s", got)
	}
}
