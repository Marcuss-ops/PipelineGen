// Package asset — sha256idempotency.go (Stock Cutover P0 2.4 — July 2026).
//
// ValidateSHA256 + SHA256IdempotencyKey are the canonical owners of
// the "derive a stable, idempotent identifier from a SHA-256 digest"
// fact for the assets domain. All stock/youtube/artlist Build
// FinalizationRequest paths that previously did `id := prefix + ":" +
// sha[:16]` now route through SHA256IdempotencyKey (which validates
// first, slices only after) — retiring the verdict's P0 #3 panic
// risk where a short input (e.g. "a") overflowed the `[:16]` slice
// at runtime.
//
// godlike/06 SSOT — one canonical owner per fact:
//
//	"Validate canonical SHA-256 hex digest (exactly 64 lowercase
//	hex chars)" lives at internal/kernel/asset/sha256idempotency.go
//	and is exposed as the ValidateSHA256 typed helper. The two
//	derivative facts — (a) normalisation to lowercase hex, (b)
//	first-16-hex-chars prefix for idempotency-key composition —
//	live next to the validator in this same file.
//
// godlike/07 typed-error contract: ErrSHA256Invalid is the single
// sentinel (errors.New) wrapping every rejection (empty /
// wrong-length / uppercase / non-hex). Reachable via
// errors.Is(err, ErrSHA256Invalid) from every call site + test.
package asset

import (
	"errors"
	"fmt"
)

// sha256HexLen is the canonical lowercase-hex length of a
// SHA-256 digest. Bumping it (e.g. to SHA-384 / SHA-512) would
// require a coordinated deprecation record per godlike/07.
const sha256HexLen = 64

// sha256IdempPrefix is the number of hex chars (8 bytes / 64 bits)
// consumed by SHA256IdempotencyKey. 16 hex chars → 64 bits →
// ~1.8e19 distinct values; safe for `prefix:hex[:16]` dedupe
// slots as long as the input shaft dominates the pre-image space
// (which all real content digests do).
const sha256IdempPrefix = 16

// ErrSHA256Invalid is returned by ValidateSHA256 (and propagated
// via SHA256IdempotencyKey) on every rejected input. godlike/07:
// reachable via errors.Is from any caller seam — tests pin the
// contract so future wrapping layers do not break the probe.
var ErrSHA256Invalid = errors.New("asset.SHA256: invalid hex-encoded SHA-256 digest (must be exactly 64 lowercase hex chars)")

// ValidateSHA256 checks that value is a canonical hex-encoded
// SHA-256 digest: exactly 64 characters, lowercase, hex-only.
// On success returns the value unchanged (canonical-form echo).
//
// On rejection returns "" + ErrSHA256Invalid wrapped with the
// specific failure reason in the message. Canonical reasons:
//
//   - empty                            (godlike/07 no-fake-availability)
//   - len != 64                        (off-by-one or truncation bug at producer)
//   - contains non-lowercase-hex char  (uppercase A-F OR non-hex g-z / symbols)
//
// Uppercase input is REJECTED (not silently lowered to lowercase).
// Silent lowering would hide producer-side canonicalisation bugs
// at the boundary — by rejecting, this helper surfaces those bugs
// at the first read instead of at a downstream dedupe miss.
//
// Per godlike/07 typed-error contract: wrapped with %w so callers
// can errors.Is(err, asset.ErrSHA256Invalid).
func ValidateSHA256(value string) (canonical string, err error) {
	if value == "" {
		return "", fmt.Errorf("%w: empty value", ErrSHA256Invalid)
	}
	if len(value) != sha256HexLen {
		return "", fmt.Errorf("%w: len=%d (want %d)", ErrSHA256Invalid, len(value), sha256HexLen)
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		default:
			// Both uppercase (A-F) and non-hex (g-z, symbols, etc.) land here.
			// The message names byte index + offending char so operators can
			// trace producer-side non-canonicalisation points in logs.
			return "", fmt.Errorf("%w: character at byte %d (%q) is not lowercase hex", ErrSHA256Invalid, i, string(c))
		}
	}
	return value, nil
}

// SHA256IdempotencyKey returns the canonical idempotency-key string
//
//	"<prefix>:" + sha[:16]   (where sha is the 64-char lowercase hex digest)
//
// for stock / youtube / artlist BuildFinalizationRequest paths.
//
// SAFETY: this function REJECTS non-canonical inputs (empty,
// short, non-hex, uppercase) via ValidateSHA256 BEFORE slicing —
// the verdict's P0 #3 panic on `"stock:" + sha[:16]` with short
// input is permanently retired by routing through this helper.
// If ValidateSHA256 returns an error, SHA256IdempotencyKey
// propagates it wrapped with the prefix alongside (e.g.
// `SHA256IdempotencyKey("stock", "a")` returns
// `asset.SHA256IdempotencyKey("stock"): asset.SHA256: invalid
// hex-encoded SHA-256 digest (must be exactly 64 lowercase hex
// chars): len=1 (want 64)`).
//
// godlike/06 SSOT: this is the SINGLE canonical path for
// "prefix + sha[:16]" composition. Inline `prefix + ":" + sha[:16]`
// in production code is an antipattern (verdict P0 #3); use this
// helper or fail CI gate.
func SHA256IdempotencyKey(prefix, value string) (string, error) {
	canonical, err := ValidateSHA256(value)
	if err != nil {
		return "", fmt.Errorf("asset.SHA256IdempotencyKey(%q): %w", prefix, err)
	}
	return prefix + ":" + canonical[:sha256IdempPrefix], nil
}
