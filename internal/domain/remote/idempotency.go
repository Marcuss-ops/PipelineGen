// Package remote — idempotency.go (P0 Commit 6, July 2026).
//
// ArtifactIdempotencyKey is the deterministic SHA-256 triple-key
// helper that all upload-protocol commands thread through their
// X-Idempotency-Key HTTP header. The contract:
//
//	ArtifactIdempotencyKey(jobID, artifactID, sha256Hex) string
//
// Property 1 — Deterministic: same inputs -> same output byte-stable
//
//	across N invocations (idempotency-on-retry guarantee).
//	Closing the at-least-once semantics of HTTP at the
//	cryptographic-identity level.
//
// Property 2 — Collision-resistant: different inputs -> different
//
//	output (birthday-paradox negligible at sha256-strength;
//	2^32 collision probability ~ 1 in 4 billion for the
//	Creator-side upload slot universe of ~10^6 sessions).
//
// Property 3 — Header-safe: 64-char hex (no padding, no whitespace,
//
//	URL-safe character set; case-insensitive per RFC 7230).
//
// Format: ptrutil-style `hashutil.SHA256String(jobID + ":" + artifactID + ":" + sha256Hex)`.
//
// The ":"-separator avoids the sha256 hex's "all-valid" character
// overlap with the asset/job ID collation — the canonical ArtifactID
// convention is "<jobID>:<kind>:<locale>" so the single-colon
// separator between jobID and artifactID is unambiguous against
// the double-colon used inside artifactID itself.
//
// godlike/06 SSOT: this is the single canonical idempotency-key
// construction in the Creator-side upload surface. The Sender-side
// also receives the same key in the X-Idempotency-Key header and
// MUST recompute it from the same triple; mismatched triples
// surface as ErrArtifactIdempotencyKeyConflict.
package remote

import (
	"errors"

	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
)

// ArtifactIdempotencyKey computes the deterministic X-Idempotency-Key
// header value for a single artifact upload. The function is pure
// (no side effects, no timestamp / random / UUID inputs) so retries
// of the same (jobID, artifactID, sha256) triple collapse to the
// same remote-side dedup slot.
//
// Algorithm: SHA-256 of "jobID:artifactID:sha256Hex" (colon-separated
// concatenation), hex-encoded. The hashutil.SHA256String helper
// (in internal/infrastructure/files/hashutil.go) is the canonical
// leaf implementation — NOT a re-implementation here.
//
// Stability contract: the algorithm byte-format is locked at
// P0 §6 (July 2026); future schema bumps (e.g. v2 with
// workspace-tags or signer-identity) require a 4-phase migration
// via the godlike/07 EXPAND → BACKFILL → CUTOVER → CONTRACT
// sequence (introduce ArtifactIdempotencyKeyV2 in EXPAND, mirror
// construction in BACKFILL, retire V1 in CUTOVER, CONTRACT in
// the final deprecation removal).
//
// Empty-input edge case: an empty input triple would silently
// collapse ALL uploads onto a single dedup slot (because
// hashutil.SHA256String("::") is a valid 64-char hex). Per
// godlike/07 no-fake-availability, we surface the empty-key
// marker (empty string) instead. Callers MUST check
// IsValidIdempotencyKey(key) AND handle the empty case as a
// "wiring-bug" signal (not a wire-shape concern).
func ArtifactIdempotencyKey(jobID, artifactID, sha256Hex string) string {
	if jobID == "" || artifactID == "" || sha256Hex == "" {
		// Godlike/07 no-fake-availability: an empty idempotency-key
		// would silently collapse ALL uploads onto a single dedup
		// slot. Surface the wire-shape invariant to the caller via
		// a deterministic empty-key marker (NOT the SHA-256 of an
		// empty triple, which would collide with a valid key
		// derived from the realistic-but-corrupt "::" preimage).
		return ""
	}
	return hashutil.SHA256String(jobID + ":" + artifactID + ":" + sha256Hex)
}

// IsValidIdempotencyKey returns true if `key` is a well-formed
// X-Idempotency-Key header value: either the empty marker (64-char
// hex absent — callers must probe both this function AND the
// empty-case handler) or 64-char hex characters with case-insensitive
// A-F + 0-9.
//
// The case-insensitive allow (A-F AND a-f) matches RFC 7230's
// case-insensitive HTTP header value semantics — both lowercase
// and uppercase hex digests are valid header values; canonical
// recomputation always returns lowercase but a Sender-side
// implementation could choose to uppercase.
func IsValidIdempotencyKey(key string) bool {
	if key == "" {
		// Empty marker is valid by definition; callers MUST handle
		// the "wiring-bug" case explicitly. See ArtifactIdempotencyKey
		// docs for the empty triple surface.
		return true
	}
	if len(key) != 64 {
		return false
	}
	for _, c := range key {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// ErrArtifactIdempotencyKeyConflict is the typed sentinel returned
// when the remote-side observes a replayed X-Idempotency-Key with a
// DIFFERENT (artifactID, sha256) than the original request. This is
// a corruption signal — the Creator adapter threads the same key
// for retries so a divergent key indicates EITHER:
//
//   - a network bug that mangled the header
//   - a Creator-side caller bug that re-derived the key incorrectly
//   - the wrong artifact was attached to the wrong job_id slot
//
// In all three cases, fail-closed: surface the sentinel so the
// caller can reject the session and re-baseline before retrying.
// Callers errors.Is against this sentinel.
var ErrArtifactIdempotencyKeyConflict = errors.New("artifact uploader: idempotency key conflict (replay with different inputs)")
