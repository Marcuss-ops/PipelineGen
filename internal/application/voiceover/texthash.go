// Package voiceover — texthash.go (PR-VO-TYPED-PRIMITIVES, July 2026;
// PR-VO-TEXTHASH-64, August 2026).
//
// TextHash is the typed envelope for the canonical 64-char SHA-256
// fingerprint of a voiceover text.
//
// PR-VO-TEXTHASH-64 (August 2026): widened TextHash from the
// legacy 16-hex-char prefix to the full 64-char SHA-256 so both
// call paths (per-item fan-out, legacy batch) produce the same
// canonical fingerprint. The 16-char prefix caused divergent
// idempotency keys, voiceover reuse mismatches, and cache misses
// for the same text arriving from different paths. The DB column
// (voiceovers.text_hash TEXT) needs no migration — it already
// stores the full 64-char digest on the legacy batch path and
// the 16-char prefix on the per-item path. New rows always carry
// the full 64-char hash; existing 16-char rows are read-compatible
// (buildVoiceoverID re-hashes the value regardless of length).
//
// PR-VO-TYPED-PRIMITIVES (July 2026) collapsed two duplicate
// implementations that were inlined at:
//
//   - internal/application/voiceover/planner.go::truncationHash
//     (used hashutil.SHA256String(text)[:16])
//   - internal/application/voiceover/jobs/fanout.go::textHashSHA256
//     (own crypto/sha256 + encoding/hex impl)
//
// Both produced the same byte-for-byte 16-hex-char lowercase
// prefix. Post-PR-VO-TEXTHASH-64, ComputeTextHash returns the
// full 64-char hash and is byte-equivalent with the legacy
// ComputeFullTextHash (deprecated alias).
//
// JSON wire compat: type TextHash string serialises the
// underlying 64-char hex string byte-for-byte. omitempty on
// the canonical "text_hash" JSON tag works identically (zero
// value "" is omitted).
//
// DB wire compat: SQLite TEXT column binding of a TextHash
// value is byte-equivalent to binding the underlying string —
// the voiceovers.text_hash column does not need a migration.

package voiceover

import (
	"crypto/sha256"
	"encoding/hex"
)

// TextHash is the typed envelope for the 64-char SHA-256 fingerprint
// of a voiceover text. Defined type (NOT alias) so the type system
// catches the audit-flagged primitive-obsession at every assignment
// site. JSON wire shape + SQLite column shape are byte-equivalent
// with the pre-PR-VO-TYPED-PRIMITIVES string field.
//
// PR-VO-TEXTHASH-64 (August 2026): widened from 16-hex-char prefix
// to full 64-char SHA-256. The voiceovers.text_hash column (TEXT)
// already accepts 64-char values (legacy batch path). The per-item
// fan-out path now emits the full hash.
//
// Conversion semantics: a TextHash value IS a string at the
// any level (Go's named-type rules) so the metaBuf-style
// `meta["text_hash"] = textHash` assignment + JSON marshalling + DB
// binding all work without explicit conversion. The typed surface
// is the compile-time guarantee; the wire surface is unchanged.
type TextHash string

// EmptyTextHash is the canonical zero value. Use this (NOT the
// string-literal "") for typed comparison so the audit-pin
// discipline catches a future drift on the empty-marker.
const EmptyTextHash TextHash = ""

// ComputeTextHash returns the canonical 64-char SHA-256 hex digest
// of text. Byte-stable across retries with the same input (per
// godlike/07 no-fake-availability).
//
// PR-VO-TEXTHASH-64 (August 2026): widened from 16-hex-char prefix
// to full 64-char digest. Both the per-item fan-out path and the
// legacy batch path now produce the same canonical fingerprint, so
// idempotency keys, voiceover reuse, and cache lookups match across
// both paths.
//
// Empty-text input returns EmptyTextHash.
func ComputeTextHash(text string) TextHash {
	return TextHash(sha256Hex(text))
}

// String returns the underlying string form. Useful for fmt
// interpolation (`fmt.Sprintf("text=%s", hash)`) where the
// named-type rules would otherwise require explicit conversion.
//
// NOT a Stringer conformance declaration — the voiceover package
// already has a higher-layer String form (FinalizeResult fields)
// and the audit's minimal-blast-radius discipline avoids
// opportunistic Stringer additions.
func (t TextHash) String() string { return string(t) }

// sha256Hex returns the full 64-char SHA-256 hex digest of text.
// Uses crypto/sha256 directly so the voiceover package stays free of
// any internal/infrastructure/files import (Pattern 0, AGENTS.md).
// Empty-text input returns empty string (defensive).
func sha256Hex(text string) string {
	if text == "" {
		return ""
	}
	h := sha256.New()
	h.Write([]byte(text))
	return hex.EncodeToString(h.Sum(nil))
}

// ComputeFullTextHash returns the full 64-char SHA-256 hex digest of
// text. Deprecated: use ComputeTextHash instead (widened to full
// 64-char per PR-VO-TEXTHASH-64). Kept as a one-line alias so
// existing callers in stages.go compile without churn.
//
// Empty-text input returns empty string (defensive).
func ComputeFullTextHash(text string) string {
	return string(ComputeTextHash(text))
}

// IsEmpty returns true when t is the canonical zero value.
// Convenience predicate for `if !hash.IsEmpty()` checks (more
// readable than `if hash != ""` for a typed envelope).
func (t TextHash) IsEmpty() bool { return t == EmptyTextHash }
