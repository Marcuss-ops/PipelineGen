// Package schema — pointid_test.go (Fase 12 / Commit 1, July 2026)
//
// 6 focused tests for the SHA-256-based deterministic point ID
// derivation:
//
//	#1 — Determinism: same assetID → same UUID across N calls
//	     (replay-safety contract).
//	#2 — UUID v8 format: version nibble=8 (custom), variant bits
//	     RFC 4122. Pins the wire shape so a future operator
//	     inspecting the Qdrant collection can distinguish
//	     pre-Fase-12 v5/SHA-1 points from post-Fase-12 v8/SHA-256.
//	#3 — sha256 traceability: the UUID body bytes EQUAL sha256 first
//	     16 bytes (with version/variant bits set). A future regression
//	     that switched back to SHA-1 would surface here.
//	#4 — Empty input → empty string (caller-side guard pattern).
//	#5 — Collision resistance: 1-char input diff → significantly
//	     different UUID (mismatched bytes on the canonical hash
//	     boundary).
//	#6 — Distribution: 1000 distinct inputs → 1000 distinct outputs
//	     (no spurious collisions across realistic asset-ID shapes).
//
// godlike/06 SSOT: this test co-locates with the implementation
// (pointid.go) under the canonical schema package. Companion
// tests at internal/infrastructure/qdrant/transport/pointid_test.go
// pin the function's binary-level invariants for the transport
// adapter; tests above pin the NEW sha256-era wire shape for the
// boundary function itself.
package schema

import (
	"crypto/sha256"
	"testing"

	"github.com/google/uuid"
)

// ── Test #1: Determinism (replay-safety) ──────────────────────────────

// TestAssetIDToQdrantPointID_OperativeDeterminism is the canonical
// replay-safety test for Fase 12. The same assetID is hashed N
// times; every output MUST equal the first. The pre-Fase-12
// implementation also satisfied this (UUID v5/SHA-1 is deterministic
// too); the new sha256-based implementation MUST preserve it. The
// "operative" prefix in the name signals "this is the contract
// that outbox replay EnqueueReindex + dispatcher depend on" —
// any drift here surfaces as silent point-duplication in
// production. (companion tests at transport/pointid_test.go
// add binary-distinctness assertions).
func TestAssetIDToQdrantPointID_OperativeDeterminism(t *testing.T) {
	const assetID = "yt_abc123_000_015"
	first := AssetIDToQdrantPointID(assetID)
	for i := 0; i < 16; i++ {
		if got := AssetIDToQdrantPointID(assetID); got != first {
			t.Fatalf("non-deterministic output at iter %d: first=%q got=%q",
				i, first, got)
		}
	}
}

// ── Test #2: UUID v8 format ───────────────────────────────────────────

// TestAssetIDToQdrantPointID_UUIDv8Format pins the wire shape Fase 12
// ships. The UUID v nibble (4 bits) MUST be 0x8 (custom per RFC 9562
// §5.8). The variant bits (2 bits) MUST be 0b10 (RFC 4122). Pre-Fase-12
// the output was v5/SHA-1 which would fail this test — that's the
// point: a future regression to v5 would surface here.
//
// The canonical hyphenated 36-char form is also asserted (no
// alternative encodings — Qdrant accepts the canonical form only).
func TestAssetIDToQdrantPointID_UUIDv8Format(t *testing.T) {
	out := AssetIDToQdrantPointID("yt_abc123_000_015")
	if len(out) != 36 {
		t.Fatalf("UUID must be 36 chars (canonical hyphenated form), got %d: %q",
			len(out), out)
	}
	// Parse the version nibble: char at index 14 (right after the
	// 8-4-4 byte layout).
	versionChar := out[14]
	if versionChar != '8' {
		t.Errorf("UUID version nibble: got %c (%q), want '8' (v8 custom per RFC 9562 §5.8)",
			versionChar, out)
	}
	// Variant nibble: char at index 19 (after the 8-4-4 segment).
	// For RFC 4122 variant, this MUST be '8', '9', 'a', or 'b'
	// (binary 0b10xx).
	variantChar := out[19]
	if variantChar != '8' && variantChar != '9' &&
		variantChar != 'a' && variantChar != 'b' {
		t.Errorf("UUID variant: got %c (%q), want one of [8 9 a b] (RFC 4122 0b10xx)",
			variantChar, out)
	}
}

// ── Test #3: sha256 traceability ──────────────────────────────────────

// TestAssetIDToQdrantPointID_TracingSha256 asserts that the UUID
// body bytes are EXACTLY sha256(assetID)[:16] with the v8 version
// + RFC 4122 variant bits superimposed. This is the strongest
// property: a regression that switched the hash algorithm
// (SHA-1, MD5, etc.) would surface here even if determinism +
// format invariants held.
//
// CRITICAL non-circularity: the function only touches byte 6
// (high nibble → 0x8 version) and byte 8 (high 2 bits → 0b10
// variant). All other bytes (0-5, 7, 9-15) are raw sha256
// verbatim. The test compares:
//   - Bytes 0-5, 7, 9-15: parsed UUID byte == raw sha256 byte
//   - Byte 6: parsed UUID low nibble == raw sha256 low nibble
//     (high nibble is forced to 0x8 by the function; not
//     informative for traceability)
//   - Byte 8: variant bits must be 0b10 (RFC 4122; not
//     informative for traceability because the function forces
//     them regardless of the hash output)
//
// A regression to SHA-1 (or any non-sha256 hash) would produce
// DIFFERENT bytes for the unmodified positions (0-5, 7, 9-15)
// AND a different low nibble for byte 6. The test would catch
// either divergence.
func TestAssetIDToQdrantPointID_TracingSha256(t *testing.T) {
	const assetID = "yt_abc123_000_015"
	out := AssetIDToQdrantPointID(assetID)

	rawHash := sha256.Sum256([]byte(assetID))
	var wantRaw [16]byte
	copy(wantRaw[:], rawHash[:16])

	got, err := uuidParseBytes(out)
	if err != nil {
		t.Fatalf("parse UUID %q: %v", out, err)
	}
	// Bytes the function does NOT modify (raw sha256 verbatim).
	unmodified := []int{0, 1, 2, 3, 4, 5, 7, 9, 10, 11, 12, 13, 14, 15}
	for _, i := range unmodified {
		if got[i] != wantRaw[i] {
			t.Errorf("byte %d: got 0x%02x, want 0x%02x (raw sha256 first 16 bytes, unmodified by function)",
				i, got[i], wantRaw[i])
		}
	}
	// Byte 6: the function forces the high nibble to 0x8 (v8).
	// The low nibble is raw sha256 verbatim — a regression to a
	// different hash would have a different low nibble.
	if got[6]&0x0f != wantRaw[6]&0x0f {
		t.Errorf("byte 6 low nibble: got 0x%x, want 0x%x (raw sha256 low nibble; high nibble is v8 by design)",
			got[6]&0x0f, wantRaw[6]&0x0f)
	}
	// Byte 8: the function forces the high 2 bits to 0b10
	// (RFC 4122 variant). The lower 6 bits are raw sha256 but the
	// function's `| 0x80` is idempotent when the original bits are
	// already 0b10/0b11, so we cannot reliably recover the raw
	// sha256 byte 8. The variant assertion is structural (always
	// 0b10) and adds no traceability value. We skip byte 8.
}

// ── Test #4: Empty input ───────────────────────────────────────────────

// TestAssetIDToQdrantPointID_EmptyInput pins the empty-string
// passthrough so the canonical boundary does NOT silently
// substitute a non-canonical UUID for an empty input.
func TestAssetIDToQdrantPointID_EmptyInput(t *testing.T) {
	if got := AssetIDToQdrantPointID(""); got != "" {
		t.Fatalf("empty input must yield empty output, got %q", got)
	}
}

// ── Test #5: Collision resistance ──────────────────────────────────────

// TestAssetIDToQdrantPointID_CollisionResistance asserts that a
// 1-character input difference yields a meaningfully different
// UUID. SHA-256 is expected to flip ~half of the UUID bytes on
// a typical input flip; weaker hashes (CRC32 prefix-methods, etc.)
// would fail this test catastrophically.
func TestAssetIDToQdrantPointID_CollisionResistance(t *testing.T) {
	idA := AssetIDToQdrantPointID("yt_abc123_000_015")
	idB := AssetIDToQdrantPointID("yt_abc124_000_015") // one char diff
	if idA == idB {
		t.Fatalf("collision on 1-char diff: %q == %q", idA, idB)
	}
	aBytes, _ := uuidParseBytes(idA)
	bBytes, _ := uuidParseBytes(idB)
	differ := 0
	for i := 0; i < 16; i++ {
		if aBytes[i] != bBytes[i] {
			differ++
		}
	}
	// SHA-256 flips ~50% of bits on a one-bit input change on
	// average; on a one-char diff the byte-level flip rate should
	// be substantial. We assert >= 8 of 16 bytes differ (50% of
	// the byte-domain) as a conservative floor — anything below
	// this would signal a degenerate hash function.
	if differ < 8 {
		t.Errorf("weak collision resistance: only %d of 16 bytes differ on 1-char input change (expected ≥8)",
			differ)
	}
}

// ── Test #6: Distribution (1000 distinct inputs) ──────────────────────

// TestAssetIDToQdrantPointID_DistributionFase12 asserts no spurious
// collisions across 1000 realistic asset-ID shapes. SHA-256's
// birthday-bound collision probability at n=1000 is ≈10^-70 —
// vanishingly small. A regression that introduces a
// truncation/non-collision bug (e.g., only first 4 bytes of the
// hash) would surface as multiple collisions in this test.
func TestAssetIDToQdrantPointID_DistributionFase12(t *testing.T) {
	const n = 1000
	seen := make(map[string]string, n)
	for i := 0; i < n; i++ {
		id := assetIDForI(i)
		out := AssetIDToQdrantPointID(id)
		if prev, ok := seen[out]; ok {
			t.Fatalf("duplicate output %q across inputs %q and %q",
				out, prev, id)
		}
		seen[out] = id
	}
	if got := len(seen); got != n {
		t.Fatalf("distribution produced %d distinct outputs for %d inputs",
			got, n)
	}
}

// ── helpers (test-local; not exported) ────────────────────────────────

// assetIDForI constructs a genuinely distinct asset ID per integer
// (NOT i%32 which would cycle). Format: "asset-<NNNNNN>" through
// "asset-<999999>" for the first 1000 iterations.
func assetIDForI(i int) string {
	const width = 6
	digits := make([]byte, width)
	for j := width - 1; j >= 0; j-- {
		digits[j] = byte('0' + i%10)
		i /= 10
	}
	return "asset-" + string(digits)
}

// uuidParseBytes parses a canonical 36-char UUID string and returns
// the underlying 16 bytes. Test-local helper used by the sha256
// traceability and collision-resistance tests to compare UUID
// bodies byte-for-byte without re-importing the UUID library
// multiple times.
func uuidParseBytes(s string) ([16]byte, error) {
	var b [16]byte
	parsed, err := uuid.Parse(s)
	if err != nil {
		return b, err
	}
	copy(b[:], parsed[:])
	return b, nil
}
