// Package document — usecase_test.go (P0 #3-B residual regression pin, July 2026).
//
// TDD coverage for buildDocArtifactIdempotencyKey — the typed-package
// entry-point over the canonical asset.SHA256IdempotencyKey validator
// (godlike/06 SSOT). The pre-migration site at usecase.go:189 did the
// panic-prone `"doc-" + info.SHA256[:16]` literal without validation —
// Stock §12-1 (July 2026) closed the runtime-panic class on stock side;
// these tests pin the same closure contract on the document side
// WITHOUT requiring the full spine wiring (mocked DeliveryPublisher /
// SpineDB / SpineFinalizer).
//
// Test cases mirror finalizer_gates_test.go::TestVerifyChunks_RejectsMalformedSHA256
// + TestVerifyMetadata_RejectsMalformedSHA256 — the canonical surface
// the verdict (and AGENTS.md §Known Issues & Fixes #12 closure pattern)
// pins for stock. Tests live in package document so they can reach the
// package-private buildDocArtifactIdempotencyKey without an exported
// surface.
package document

import (
	"errors"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// canonicalHex64 is a 64-char lowercase hex digest fixture (deterministic
// per-index so test diff stays stable). Mirrors finalizer_gates_test.go::fakeSHA.
func canonicalHex64(i int) string {
	const pad = "0123456789abcdef"
	out := make([]byte, 64)
	for k := 0; k < 64; k++ {
		out[k] = pad[(k+i)%len(pad)]
	}
	return string(out)
}

// ── Verdict-test cases (P0 #3) ──────────────────────────────────────

// TestBuildDocArtifactIdempotencyKey_RejectsEmpty pins the empty-input
// case: the canonical validator (godlike/07 no-fake-availability) MUST
// surface ErrSHA256Invalid rather than silently return an empty key
// (which would silently dedupe unique documents onto the same Drive slot).
func TestBuildDocArtifactIdempotencyKey_RejectsEmpty(t *testing.T) {
	_, err := buildDocArtifactIdempotencyKey("")
	if err == nil {
		t.Fatal("empty SHA: want error, got nil")
	}
	if !errors.Is(err, asset.ErrSHA256Invalid) {
		t.Errorf("err = %v; want errors.Is(asset.ErrSHA256Invalid) == true", err)
	}
}

// TestBuildDocArtifactIdempotencyKey_RejectsLength1 pins the len=1
// panic class: `"doc-" + sha[:16]` would PANIC at runtime (Go slice
// bounds: max index 15 exceeds string length). The helper rejects the
// input cleanly via ErrSHA256Invalid BEFORE any slicing.
func TestBuildDocArtifactIdempotencyKey_RejectsLength1(t *testing.T) {
	_, err := buildDocArtifactIdempotencyKey("a")
	if err == nil {
		t.Fatal("length-1 SHA: want error, got nil")
	}
	if !errors.Is(err, asset.ErrSHA256Invalid) {
		t.Errorf("err = %v; want errors.Is(asset.ErrSHA256Invalid) == true", err)
	}
}

// TestBuildDocArtifactIdempotencyKey_RejectsLength15 documents the
// exact verdict-spec case ("hash lungo 15"): length < 16 triggers
// the slice-bound panic on the legacy literal. Helper returns typed
// error without panic.
func TestBuildDocArtifactIdempotencyKey_RejectsLength15(t *testing.T) {
	_, err := buildDocArtifactIdempotencyKey(strings.Repeat("a", 15))
	if err == nil {
		t.Fatal("length-15 SHA: want error, got nil")
	}
	if !errors.Is(err, asset.ErrSHA256Invalid) {
		t.Errorf("err = %v; want errors.Is(asset.ErrSHA256Invalid) == true", err)
	}
}

// TestBuildDocArtifactIdempotencyKey_RejectsLength63 documents the
// exact verdict-spec case ("hash lungo 63"): length = 63 is one short
// of the canonical 64-char span; slicing on the legacy literal would
// skip the final hex char but NOT panic (no bounds violation). Helper
// rejects the input cleanly so the canonical 64-char enforcement is
// not silently violated (a len=63 fingerprint would dedupe onto a
// DIFFERENT slot than its len=64 neighbour — silent Drive-side
// boundary collision).
func TestBuildDocArtifactIdempotencyKey_RejectsLength63(t *testing.T) {
	_, err := buildDocArtifactIdempotencyKey(canonicalHex64(0)[:63])
	if err == nil {
		t.Fatal("length-63 SHA: want error, got nil")
	}
	if !errors.Is(err, asset.ErrSHA256Invalid) {
		t.Errorf("err = %v; want errors.Is(asset.ErrSHA256Invalid) == true", err)
	}
}

// TestBuildDocArtifactIdempotencyKey_RejectsNonHex pins the
// "hash non esadecimale" verdict-spec case: a 64-char string with one
// non-hex char ('g' = the verdict's chosen sentinel). The legacy
// literal `"doc-" + sha[:16]` would silently accept the non-hex
// fingerprint (no validation) and dedupe the run onto a Drive slot
// derived from a non-canonical string. The helper rejects it.
func TestBuildDocArtifactIdempotencyKey_RejectsNonHex(t *testing.T) {
	bad := canonicalHex64(0)
	bad = bad[:63] + "g" // 64 chars, last byte is non-hex
	_, err := buildDocArtifactIdempotencyKey(bad)
	if err == nil {
		t.Fatalf("non-hex SHA: want error, got nil (input=%q)", bad)
	}
	if !errors.Is(err, asset.ErrSHA256Invalid) {
		t.Errorf("err = %v; want errors.Is(asset.ErrSHA256Invalid) == true", err)
	}
}

// TestBuildDocArtifactIdempotencyKey_RejectsUppercase pins the
// "hash uppercase" verdict-spec case: SHA256String always emits
// lowercase hex (Go stdlib encoding/hex.EncodeToString default), but
// a future SHA producer that switches to uppercase would silently
// produce different idempotency keys per attempt. The canonical
// validator REJECTS uppercase — surfaces producer-side drift at the
// first read, not at a later implicit-key-mismatch dedupe miss.
// Silent lowering is FORBIDDEN per asset.ValidateSHA256 godblock.
func TestBuildDocArtifactIdempotencyKey_RejectsUppercase(t *testing.T) {
	_, err := buildDocArtifactIdempotencyKey(strings.ToUpper(canonicalHex64(0)))
	if err == nil {
		t.Fatal("uppercase SHA: want error, got nil")
	}
	if !errors.Is(err, asset.ErrSHA256Invalid) {
		t.Errorf("err = %v; want errors.Is(asset.ErrSHA256Invalid) == true", err)
	}
}

// ── Verdict-test case happy (P0 #3 #6 "hex valido") ────────────────

// TestBuildDocArtifactIdempotencyKey_Happy64Hex pins the positive
// contract: the verdict-spec "hex valido" case. A canonical 64-char
// lowercase hex digest MUST produce `"doc:<sha[:16]>"` byte-stable.
// The pre-migration literal `"doc-" + info.SHA256[:16]` would yield
// byte-equal output for a canonical input — so this test pins format
// parity (no prefix drift) + length-stability.
func TestBuildDocArtifactIdempotencyKey_Happy64Hex(t *testing.T) {
	sha := canonicalHex64(0) // canonical 64-char lowercase hex
	got, err := buildDocArtifactIdempotencyKey(sha)
	if err != nil {
		t.Fatalf("happy path: want nil err, got %v", err)
	}
	want := "doc:" + sha[:16]
	if got != want {
		t.Fatalf("happy path: want %q, got %q", want, got)
	}
	if len(got) != len("doc:")+16 {
		t.Fatalf("happy path: len=%d, want %d (doc:%d hex chars)", len(got), len("doc:")+16, 16)
	}
}

// ── Idempotency across retries (godlike/06 SSOT byte-stability) ───

// TestBuildDocArtifactIdempotencyKey_IdempotentAcrossRetries pins
// the byte-stability requirement for retry-path SSOT. A retry on
// transient publisher/upload failure MUST produce a byte-equal
// idempotency key — the publisher dedup slot collapses retries onto
// the same Drive file via this key. Pre-migration literal was
// already byte-stable on canonical input; this test pins the
// post-migration contract so future refactors of the helper don't
// silently break the retry path.
func TestBuildDocArtifactIdempotencyKey_IdempotentAcrossRetries(t *testing.T) {
	sha := canonicalHex64(7)
	seen := make(map[string]int)
	for i := 0; i < 1000; i++ {
		got, err := buildDocArtifactIdempotencyKey(sha)
		if err != nil {
			t.Fatalf("retry %d: want nil err, got %v", i, err)
		}
		seen[got]++
	}
	if len(seen) != 1 {
		t.Fatalf("idempotency violated: %d distinct keys across 1000 retries (sha=%q)", len(seen), sha)
	}
	for k, n := range seen {
		if n != 1000 {
			t.Errorf("key %q: count=%d, want 1000", k, n)
		}
	}
}
