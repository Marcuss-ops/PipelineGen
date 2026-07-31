// Package remote_test — idempotency_factory_test.go (Commit D, July 2026).
//
// Unit tests locking the constructor-injection pattern introduced by
// Commit D. Exercises MakeArtifactIdempotencyKey + MakeCompleteJobIdempotencyKey
// with FAKE HashFunc values — no production crypto dependency in tests.
//
// Per Commit D user-spec literal: "Aggiungi un test unit con fake `HashFunc`".
// The test fake asserts the typed-port contract:
//  1. Any `func(string) string` value satisfies hashutil.HashFunc
//     (structural typing on function types — Go-idiomatic).
//  2. The fake-injected path produces fake byte-patterns in derived
//     keys (NOT the real SHA-256 hex).
//  3. Deterministic + Pure + byte-stable contract holds under fake
//     injection (idempotency-on-retry guarantee).
//  4. Nil HashFunc at construction panics (godlike/07 fail-closed).
package remote_test

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/remote"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/remote/hashutil"
)

// testFakeHasher returns "FAKE:<input>" for every call. Exercises the
// Commit D typed-port contract — any func(string) string satisfying
// hashutil.HashFunc must work, no explicit "implements" clause required.
func testFakeHasher(s string) string {
	return "FAKE:" + s
}

// Compile-time assertion (godlike/06 typed-port ownership):
// `testFakeHasher` must satisfy `hashutil.HashFunc`. If HashFunc is ever
// converted to an interface with methods, this file fails to build.
var _ hashutil.HashFunc = testFakeHasher

// TestMakeArtifactIdempotencyKey_FakeHash_ProducesFakeBytes pins the
// Commit D injection contract: a FAKE hasher wired into the constructor
// MUST produce the fake byte-pattern in the derived idempotency key —
// NOT the real SHA-256. This is the failure mode godlike/07
// no-fake-availability FORBIDS: production code must NEVER observe a
// fake-injected path if the composition root is correctly wiring
// the standard-library SHA256String default.
func TestMakeArtifactIdempotencyKey_FakeHash_ProducesFakeBytes(t *testing.T) {
	derive := remote.MakeArtifactIdempotencyKey(testFakeHasher)
	got := derive("j-1", "a-1", "h-1")
	want := "FAKE:j-1:a-1:h-1"
	if got != want {
		t.Errorf("fake-hash derivation: got %q, want %q (Commit D injection contract violated)", got, want)
	}
}

// TestMakeArtifactIdempotencyKey_FakeHash_StableAcrossRetries mirrors
// the production deterministic + byte-stable contract under
// constructor injection. A canonical idempotency-key surface must
// collapse N invocations of the SAME triple to ONE byte pattern
// (idempotency-on-retry guarantee).
func TestMakeArtifactIdempotencyKey_FakeHash_StableAcrossRetries(t *testing.T) {
	derive := remote.MakeArtifactIdempotencyKey(testFakeHasher)
	const N = 1000
	key1 := derive("j-1", "a-1", "h-1")
	for i := 0; i < N; i++ {
		if got := derive("j-1", "a-1", "h-1"); got != key1 {
			t.Fatalf("iteration %d: key drift (%q vs %q) — non-deterministic fake violates idempotency-on-retry contract", i, got, key1)
		}
	}
}

// TestMakeArtifactIdempotencyKey_EmptyInputsReturnEmptyMarker pins the
// godlike/07 no-fake-availability contract: an empty input triple MUST
// produce empty string (NOT a valid-looking hash that would silently
// collapse all uploads onto a single dedup slot). Validates that the
// factory preserves the empty-marker gate regardless of the injected
// hasher (even a fake).
func TestMakeArtifactIdempotencyKey_EmptyInputsReturnEmptyMarker(t *testing.T) {
	derive := remote.MakeArtifactIdempotencyKey(testFakeHasher)
	cases := []struct {
		name                         string
		jobID, artifactID, sha256Hex string
	}{
		{"empty-jobID", "", "a-1", "h-1"},
		{"empty-artifactID", "j-1", "", "h-1"},
		{"empty-sha256Hex", "j-1", "a-1", ""},
		{"all-empty", "", "", ""},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if got := derive(c.jobID, c.artifactID, c.sha256Hex); got != "" {
				t.Errorf("expected empty marker (godlike/07), got %q", got)
			}
		})
	}
}

// TestMakeArtifactIdempotencyKey_PanicOnNilHash pins the godlike/07
// fail-closed contract: nil HashFunc at construction MUST panic —
// the composition root must never call this with nil (fail-closed
// at boot, not at first request).
func TestMakeArtifactIdempotencyKey_PanicOnNilHash(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on nil HashFunc (godlike/07 fail-closed)")
		}
	}()
	_ = remote.MakeArtifactIdempotencyKey(hashutil.HashFunc(nil))
}

// TestMakeCompleteJobIdempotencyKey_FakeHash_ProducesFakeBytes —
// parallel test for the (jobID, attempt, resultHash) derivation. Same
// fake contract; verifies the preimage format
// (jobID:attempt:resultHash) routes through the closure correctly.
func TestMakeCompleteJobIdempotencyKey_FakeHash_ProducesFakeBytes(t *testing.T) {
	derive := remote.MakeCompleteJobIdempotencyKey(testFakeHasher)
	got := derive("j-1", 0, "h-1")
	want := "FAKE:j-1:0:h-1"
	if got != want {
		t.Errorf("fake-hash job-completion derivation: got %q, want %q (Commit D injection contract violated)", got, want)
	}
}

// TestMakeCompleteJobIdempotencyKey_DifferentInputShapes pins that
// attempt values 0, 1, 2 produce distinct fake byte outputs (mirrors
// the production property that attempt differences → different keys).
func TestMakeCompleteJobIdempotencyKey_DifferentInputShapes(t *testing.T) {
	derive := remote.MakeCompleteJobIdempotencyKey(testFakeHasher)
	a := derive("j-1", 0, "h-1")
	b := derive("j-1", 1, "h-1") // different attempt
	c := derive("j-2", 0, "h-1") // different jobID
	d := derive("j-1", 0, "h-2") // different hash
	if a == b || a == c || a == d {
		t.Errorf("expected distinct keys for distinct inputs; got a=%q b=%q c=%q d=%q", a, b, c, d)
	}
}

// TestMakeCompleteJobIdempotencyKey_EmptyInputsReturnEmptyMarker —
// empty jobID/resultHash + negative attempt all surface empty marker
// regardless of injected hasher (even a fake).
func TestMakeCompleteJobIdempotencyKey_EmptyInputsReturnEmptyMarker(t *testing.T) {
	derive := remote.MakeCompleteJobIdempotencyKey(testFakeHasher)
	cases := []struct {
		name    string
		jobID   string
		attempt int
		hash    string
	}{
		{"empty-jobID", "", 0, "h-1"},
		{"empty-hash", "j-1", 0, ""},
		{"negative-attempt", "j-1", -1, "h-1"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if got := derive(c.jobID, c.attempt, c.hash); got != "" {
				t.Errorf("expected empty marker (godlike/07), got %q", got)
			}
		})
	}
}

// TestMakeCompleteJobIdempotencyKey_PanicOnNilHash — parallel panic-on-nil.
func TestMakeCompleteJobIdempotencyKey_PanicOnNilHash(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on nil HashFunc (godlike/07 fail-closed)")
		}
	}()
	_ = remote.MakeCompleteJobIdempotencyKey(hashutil.HashFunc(nil))
}

func TestArtifactIdempotencyKey_DefaultMatchesInjectedHasher(t *testing.T) {
	derive := remote.MakeArtifactIdempotencyKey(hashutil.SHA256String)
	want := derive("j-1", "a-1", "h-1")
	if got := remote.ArtifactIdempotencyKey("j-1", "a-1", "h-1"); got != want {
		t.Fatalf("legacy artifact key = %q, injected default = %q; compatibility drift", got, want)
	}
}

func TestCompleteJobIdempotencyKey_DefaultMatchesInjectedHasher(t *testing.T) {
	derive := remote.MakeCompleteJobIdempotencyKey(hashutil.SHA256String)
	want := derive("j-1", 2, "h-1")
	if got := remote.CompleteJobIdempotencyKey("j-1", 2, "h-1"); got != want {
		t.Fatalf("legacy complete-job key = %q, injected default = %q; compatibility drift", got, want)
	}
}

// TestArtifactIdempotencyKey_LegacyFreeFunction_StillWorks pins the
// back-compat contract: the legacy free function ArtifactIdempotencyKey
// continues to return a 64-char hex value (production SHA-256 via the
// package-level defaultArtifactKey) for downstream callers that have
// NOT migrated to MakeArtifactIdempotencyKey yet. This guard ensures
// Commit D's compat-surface is valid — downstream consumers
// (creator.adapter, completion.publish_verified, etc.) do not need
// to migrate in lockstep with the domain refactor.
func TestArtifactIdempotencyKey_LegacyFreeFunction_StillWorks(t *testing.T) {
	key := remote.ArtifactIdempotencyKey("j-1", "a-1", "h-1")
	if key == "" {
		t.Error("legacy free function returned empty marker (production defaultArtifactKey NOT wired — this is a Composition-time wiring bug)")
	}
	if len(key) != 64 {
		t.Errorf("legacy free function returned non-64-char hex: got len=%d key=%q", len(key), key)
	}
	if !strings.ContainsFunc(key, func(r rune) bool {
		return (r < '0' || r > '9') && (r < 'a' || r > 'f')
	}) == false {
		// The condition is inverted for readability; we want to assert ALL chars are [0-9a-f]
		// Sanity check: the canonical SHA-256 produces lowercase hex.
		for _, r := range key {
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
				t.Errorf("legacy free function returned non-lowercase-hex char: %q (rune=%q)", key, r)
				break
			}
		}
	}
}

// TestCompleteJobIdempotencyKey_LegacyFreeFunction_StillWorks — parallel
// back-compat check for the (jobID, attempt, resultHash) surface.
func TestCompleteJobIdempotencyKey_LegacyFreeFunction_StillWorks(t *testing.T) {
	key := remote.CompleteJobIdempotencyKey("j-1", 0, "h-1")
	if key == "" {
		t.Error("legacy free function returned empty marker (production defaultCompleteJobKey NOT wired)")
	}
	if len(key) != 64 {
		t.Errorf("legacy free function returned non-64-char hex: got len=%d key=%q", len(key), key)
	}
}
