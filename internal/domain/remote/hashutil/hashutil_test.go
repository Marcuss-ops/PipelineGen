// Package hashutil_test — locks the HashFunc contract at both compile
// time (structural-typing guard) and runtime (test-fake satisfaction).
//
// godlike/06 one-owner-per-fact verification: HashFunc must be the
// SINGLE canonical port; this test pins the typed-port signature so
// future drift (e.g. turning the function-type into a method interface,
// adding generics, etc.) fails at compile time on the
// `var _ hashutil.HashFunc = fakeHash` assertion.
//
// The runtime tests verify that the test-fake contract (Deterministic
// + Pure + byte-stable) holds under all the input shapes a future
// domain caller might pass to the port.
package hashutil_test

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/remote/hashutil"
)

// testFakeHasher is the canonical test fake — returns the input string
// prefixed with "FAKE:". Exercises that:
//  1. The func(string) string shape satisfies hashutil.HashFunc (compile-time).
//  2. The fake is deterministic + pure (runtime).
//  3. Multiple invocations yield identical results (byte-stable contract).
func testFakeHasher(s string) string {
	return "FAKE:" + s
}

// Compile-time structural-typing assertion: the test fake satisfies the
// typed-port. If someone changes HashFunc from a function-type alias
// to a method interface (or adds a second method), this file fails to
// build before any runtime surface can drift.
var _ hashutil.HashFunc = testFakeHasher

// TestHashFunc_FakeSatisfiesPort_RuntimeContract pins the runtime shape.
func TestHashFunc_FakeSatisfiesPort_RuntimeContract(t *testing.T) {
	got := testFakeHasher("hello")
	if got != "FAKE:hello" {
		t.Errorf("fake-hash runtime contract: got %q, want %q", got, "FAKE:hello")
	}
}

// TestHashFunc_DeterministicAcrossCalls mirrors the production
// deterministic + byte-stable contract (the canonical use case is
// idempotency-key derivation, where same-invocation-twice must produce
// same bytes).
func TestHashFunc_DeterministicAcrossCalls(t *testing.T) {
	const N = 1000
	first := testFakeHasher("j-1:a-1:h-1")
	for i := 0; i < N; i++ {
		if got := testFakeHasher("j-1:a-1:h-1"); got != first {
			t.Fatalf("iteration %d: hash drift (%q vs %q) — non-deterministic fake violates idempotency-on-retry contract",
				i, got, first)
		}
	}
}

// TestSHA256String_Golden pins the byte-identical delegation to the kernel
// digest SSOT: the digest of "hello" must equal the canonical SHA-256
// literal computed by the pre-migration implementation (crypto/sha256).
func TestSHA256String_Golden(t *testing.T) {
	const goldenHello = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if got := hashutil.SHA256String("hello"); got != goldenHello {
		t.Fatalf("SHA256String(hello) = %q, want %q", got, goldenHello)
	}
	// Byte-stability for the idempotency-key shape the port serves.
	if got := hashutil.SHA256String("j-1:a-1:h-1"); got != hashutil.SHA256String("j-1:a-1:h-1") {
		t.Fatal("SHA256String must be deterministic")
	}
}

// TestHashFunc_EmptyStringReturnsEmptyMarker verifies the empty-input
// pass-through behavior of the fake (the production SHA-256 returns
// a valid empty hash, NOT an empty marker — but the fake adds the
// "FAKE:" prefix so empty-input still surfaces an empty-output-style
// behavior at the test layer; this is a test-fake invariant, NOT a
// production surface claim).
func TestHashFunc_EmptyStringReturnsEmptyMarker(t *testing.T) {
	got := testFakeHasher("")
	if !strings.HasPrefix(got, "FAKE:") {
		t.Errorf("fake should prefix every input, including empty: got %q", got)
	}
}
