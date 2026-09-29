package job

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestDeferredAfterCarriesTheDelayAndTheSentinel pins the two things every
// caller depends on: the sentinel probe (cheap) and the typed delay (the
// scheduler's cadence).
func TestDeferredAfterCarriesTheDelayAndTheSentinel(t *testing.T) {
	err := DeferredAfter(90*time.Second, "waiting on the remote render")
	if !errors.Is(err, ErrDeferred) {
		t.Fatalf("errors.Is(err, ErrDeferred) = false for %v", err)
	}
	deferral, ok := AsDeferral(err)
	if !ok {
		t.Fatalf("AsDeferral(%v) = false", err)
	}
	if deferral.Delay != 90*time.Second {
		t.Errorf("Delay = %s, want 90s", deferral.Delay)
	}
	if deferral.Reason != "waiting on the remote render" {
		t.Errorf("Reason = %q", deferral.Reason)
	}
}

// TestDeferralSurvivesWrapping: a handler deep in a call chain returns the
// deferral, and the layer that finalizes the job wraps it with context. The
// intent must survive — otherwise a wait is classified as a failure again.
func TestDeferralSurvivesWrapping(t *testing.T) {
	wrapped := fmt.Errorf("clip.render: settle: %w", DeferredAfter(time.Minute, "render still running"))
	deferral, ok := AsDeferral(wrapped)
	if !ok || deferral.Delay != time.Minute {
		t.Fatalf("AsDeferral(wrapped) = (%+v, %v), want the typed deferral", deferral, ok)
	}
	if !errors.Is(wrapped, ErrDeferred) {
		t.Fatal("the sentinel must survive wrapping")
	}
}

// TestDeferredWithoutADelayIsStillADeferral: the handler stated the fact (I am
// waiting) without stating the cadence. The scheduler owns the default; what
// must NOT happen is the intent being lost because Delay is zero.
func TestDeferredWithoutADelayIsStillADeferral(t *testing.T) {
	deferral, ok := AsDeferral(Deferred("provider window closed"))
	if !ok {
		t.Fatal("Deferred(reason) must be a deferral")
	}
	if deferral.Delay != 0 {
		t.Fatalf("Delay = %s, want 0 (the scheduler default applies)", deferral.Delay)
	}
}

// TestDeferralProbesAreSafeOnOrdinaryErrors: the worker runs this check on EVERY
// dispatch error, so a plain failure — including a wrapped sentinel that is not
// a deferral — must answer false without panicking.
func TestDeferralProbesAreSafeOnOrdinaryErrors(t *testing.T) {
	if _, ok := AsDeferral(nil); ok {
		t.Fatal("nil must never be a deferral")
	}
	if _, ok := AsDeferral(errors.New("render failed")); ok {
		t.Fatal("an ordinary error must not be a deferral")
	}
	if _, ok := AsDeferral(fmt.Errorf("wrapped: %w", errors.New("boom"))); ok {
		t.Fatal("a wrapped ordinary error must not be a deferral")
	}
}

// TestDeferralMessageCannotBeMistakenForAFailure: the reason is persisted as the
// job's error column, so the text must say what it is. An operator reading a
// RETRY_WAIT row has to tell "waiting" from "failed" without checking timestamps.
func TestDeferralMessageCannotBeMistakenForAFailure(t *testing.T) {
	msg := DeferredAfter(30*time.Second, "GPU lane busy").Error()
	if !strings.Contains(msg, ErrDeferred.Error()) {
		t.Fatalf("message %q does not lead with the deferral sentinel", msg)
	}
	if !strings.Contains(msg, "GPU lane busy") || !strings.Contains(msg, "30s") {
		t.Fatalf("message %q lost the reason or the delay", msg)
	}
	if bare := (&Deferral{}).Error(); bare != ErrDeferred.Error() {
		t.Fatalf("reason-less message = %q, want %q", bare, ErrDeferred.Error())
	}
}
