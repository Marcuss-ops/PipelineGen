// Package voiceover — texthash.go (PR-VO-TYPED-PRIMITIVES, July 2026).
//
// Typed envelope for the per-item text fingerprint that identifies
// a voiceover row's content (16 hex chars of SHA-256(text) prefix).
//
// PR-VO-TYPED-PRIMITIVES (July 2026) collapses two duplicate
// implementations that were inlined at:
//
//   - internal/application/voiceover/planner.go::truncationHash
//     (used hashutil.SHA256String(text)[:16])
//   - internal/application/voiceover/jobs/fanout.go::textHashSHA256
//     (own crypto/sha256 + encoding/hex impl)
//
// Both produced the same byte-for-byte 16-hex-char lowercase
// prefix per the audit-pin comment in planner.go ("hashutil.
// SHA256String returns lowercase hex (Go stdlib encoding/hex.
// EncodeToString default)"). The canonical impl below uses
// crypto/sha256 directly so the voiceover package does not
// acquire a new dependency on internal/infrastructure/files
// (Pattern 0 — port abstraction layer, June 2026).
//
// JSON wire compat: type TextHash string serialises the
// underlying 16-char hex string byte-for-byte. omitempty on
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

// textHashLen is the canonical prefix length (16 hex chars = 64 bits
// of entropy). 64 bits is sufficient for collision resistance at the
// expected row counts (~10^5 distinct texts per the audit-pin in
// planner.go::truncationHash). Renaming is HOW a future agent pins
// unannounced drift on the column-vs-row consistency contract.
const textHashLen = 16

// TextHash is the typed envelope for the 16-hex-char SHA-256 prefix
// of a voiceover text. Defined type (NOT alias) so the type system
// catches the audit-flagged primitive-obsession at every assignment
// site. JSON wire shape + SQLite column shape are byte-equivalent
// with the pre-PR-VO-TYPED-PRIMITIVES string field.
//
// Conversion semantics: a TextHash value IS a string at the
// interface{} level (Go's named-type rules) so the metaBuf-style
// `meta["text_hash"] = textHash` assignment + JSON marshalling + DB
// binding all work without explicit conversion. The typed surface
// is the compile-time guarantee; the wire surface is unchanged.
type TextHash string

// EmptyTextHash is the canonical zero value. Use this (NOT the
// string-literal "") for typed comparison so the audit-pin
// discipline catches a future drift on the empty-marker.
const EmptyTextHash TextHash = ""

// ComputeTextHash returns the canonical 16-hex-char SHA-256 prefix
// of text. Byte-stable across retries with the same input (per
// godlike/07 no-fake-availability: re-computing the same text MUST
// produce the same TextHash).
//
// Single source of truth — replaces both planner.go::truncationHash
// (which used hashutil.SHA256String) AND jobs/fanout.go::
// textHashSHA256 (which used crypto/sha256 + encoding/hex). The two
// pre-PR-VO-TYPED-PRIMITIVES implementations were byte-equivalent
// per the audit-pin comment; this canonical impl uses the stdlib
// path so the voiceover package stays free of any hashutil dep.
//
// Empty-text input returns EmptyTextHash (defensive: an empty text
// should not produce a hash that collides with a non-empty text's
// first 16 chars). Callers that need to enforce non-empty text
// should call BuildVoiceoverFilename / use the higher-layer
// Validate gates first.
func ComputeTextHash(text string) TextHash {
	if text == "" {
		return EmptyTextHash
	}
	h := sha256.New()
	h.Write([]byte(text))
	return TextHash(hex.EncodeToString(h.Sum(nil))[:textHashLen])
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

// IsEmpty returns true when t is the canonical zero value.
// Convenience predicate for `if !hash.IsEmpty()` checks (more
// readable than `if hash != ""` for a typed envelope).
func (t TextHash) IsEmpty() bool { return t == EmptyTextHash }
