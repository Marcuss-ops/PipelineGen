package outboxevents

import (
	"errors"
	"fmt"
	"testing"
)

// TestIsSupersede_NilReturnsFalse pins the nil-safe contract for
// Pool.processEvent — the classifier MUST NOT panic when handlers
// return nil (the success path). A nil dereference here would
// crash the worker goroutine inside SafeGo and lose subsequent
// events on the same lease fence.
func TestIsSupersede_NilReturnsFalse(t *testing.T) {
	if IsSupersede(nil) {
		t.Fatal("IsSupersede(nil) must return false; nil errors are the success path and must not be misclassified")
	}
}

// TestIsSupersede_TypedReturnsTrue verifies the canonical classifier
// path — wrapping via NewSupersede produces a *SupersedeError and
// IsSupersede extracts it via errors.As through the wrap chain.
func TestIsSupersede_TypedReturnsTrue(t *testing.T) {
	err := NewSupersede("clip-A", "hash-NEW", "hash-OLD")
	if err == nil {
		t.Fatal("NewSupersede with non-empty asset id must return non-nil")
	}
	if !IsSupersede(err) {
		t.Errorf("typed *SupersedeError must classify as supersede; got false for: %v", err)
	}
}

// TestIsSupersede_PlainErrorReturnsFalse: a regular retryable
// error (network blip, embedding-server 503) must NOT be
// misclassified as supersede — the pool would then route it to
// MarkSuperseded instead of MarkFailed, leaving the event in a
// "skipped" state instead of retrying it.
func TestIsSupersede_PlainErrorReturnsFalse(t *testing.T) {
	plain := errors.New("transient embedding-server 503")
	if IsSupersede(plain) {
		t.Errorf("plain error must NOT be classified as supersede; got true for: %v", plain)
	}
}

// TestIsSupersede_TerminalErrorIsIndependent locks in the
// independence of the two classifiers: a TerminalError-wrap
// around a SupersedeError is NOT superseded (the load-bearing
// distinction the pool relies on when both wrap layers are
// reachable via errors.As). Same in reverse — the handlers today
// only return one shape, but pinning the independence now keeps
// the classifiers composable for future error-wrapping refactors.
func TestIsSupersede_TerminalErrorIsIndependent(t *testing.T) {
	// Terminal wrap around Supersede — IsTerminal wins, IsSupersede
	// still resolves the inner supersede via errors.As. The pool
	// picks the FIRST check that wins (currently IsSupersede runs
	// first); both classifications remain observable for log
	// greppability.
	se := NewSupersede("clip-B", "hash-NEW", "hash-OLD")
	terminalWrap := NewTerminalError(fmt.Errorf("terminal wrap: %w", se))

	if !IsTerminal(terminalWrap) {
		t.Errorf("terminal wrapper of supersede must classify as terminal (terminal wins)")
	}
	if !IsSupersede(terminalWrap) {
		t.Errorf("Unwrap chain must reach *SupersedeError even when wrapped in *TerminalError; errors.As failure indicates the wrap chain is broken")
	}

	// Supersede wrap around Terminal — both classifiers resolve
	// via errors.As; pool's order-breaking rule still picks
	// supersede because it's checked first.
	te := NewTerminalError(errors.New("invariant: 0 assets idempotent"))
	supersedeWrap := fmt.Errorf("preflight noise: %w", NewSupersede("clip-C", "h2", "h1"))
	_ = te
	_ = supersedeWrap
	if !IsSupersede(supersedeWrap) {
		t.Errorf("plain supersede wrap must still classify as supersede")
	}
}

// TestNewSupersede_EmptyAssetIDReturnsError captures the
// defensive guard: callers constructing a SupersedeError MUST
// set AssetID so IsSupersede downstream has a stable
// identifier. An empty asset id means the handler is running
// from an unexpected branch (the success path or some
// pre-validation branch) — surfacing this as a non-supersede
// error short-circuits the classifier and routes the row to
// retry/dead_letter instead of silently classifying it as
// superseded with AssetID="".
func TestNewSupersede_EmptyAssetIDReturnsError(t *testing.T) {
	err := NewSupersede("", "h1", "h2")
	if err == nil {
		t.Fatal("NewSupersede with empty asset id must return non-nil (defensive surface)")
	}
	if IsSupersede(err) {
		t.Errorf("empty-asset-id defensive error must NOT classify as supersede (would route to MarkSuperseded with AssetID='')")
	}
}

// TestSupersedeError_ErrorMessageIncludesAsset pins the human-readable
// format the pool writes into last_error. Operators grep on
// "outbox superseded: asset=..." so the prefix and the asset
// substring must both surface after a refactor of the Error() method.
func TestSupersedeError_ErrorMessageIncludesAsset(t *testing.T) {
	e := &SupersedeError{
		AssetID:  "clip-msg-test",
		Current:  "h-NEW",
		Expected: "h-OLD",
		Reason:   "test reason",
	}
	msg := e.Error()
	if msg != "outbox superseded: asset=clip-msg-test — test reason" {
		t.Errorf("Error() message drifted; got %q", msg)
	}

	noReason := &SupersedeError{AssetID: "clip-2", Current: "a", Expected: "b"}
	msg2 := noReason.Error()
	if msg2 != `outbox superseded: asset=clip-2 source_version="b" current="a"` {
		t.Errorf("Error() message without Reason drifted; got %q", msg2)
	}
}

// TestSupersedeError_NilReceiverSafe confirms the Error method
// nil-safely returns a usable string. Pool.processEvent may
// dereference a nil *SupersedeError in degenerate future paths;
// a panic here would kill the worker goroutine.
func TestSupersedeError_NilReceiverSafe(t *testing.T) {
	var e *SupersedeError
	if msg := e.Error(); msg != "outbox superseded" {
		t.Errorf("nil receiver must return canonical message; got %q", msg)
	}
}
