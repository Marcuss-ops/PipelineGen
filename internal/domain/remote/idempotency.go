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

	domainhashutil "github.com/Marcuss-ops/PipelineGen/internal/domain/remote/hashutil"
)

// DEPRECATED (Commit A / FASE 5 follow-up, July 2026):
//
// godlike/06 SSOT — this free function is DEPRIORITIZED in favor of
// the typed-factory pattern. Future production callers SHOULD
// migrate to:
//
//	derive := remote.MakeArtifactIdempotencyKey(hashutil.HashFunc)
//	service := &ArtifactService{derive: derive, ...} // field injection
//
// (composition root wires the derive using hashutil.HashFunc
// or a test fake per the Commit D spec literal "Aggiungi un test
// unit con fake `HashFunc`").
//
// The free function remains in the surface AS-IS for two reasons:
//
//  1. Back-compat: the existing 8+ production callers
//     (creator adapter × 3 sites, publish_verified.go × 1,
//     dedup_tdd_test.go × 1, completion_e2e_test.go × 1,
//     publish_verified_test.go × 4, creator adapter_test.go × 2,
//     artifact_uploader_test.go × 6) thread this free function
//     for the legacy X-Idempotency-Key derivation. Removing the
//     free function in Commit A would force ALL callers to migrate
//     in a single PR — godlike/06 minimum-blast-radius forbids
//     this. The DEPRECATED note directs future migrations.
//
//  2. Default-priming: the package-init `defaultArtifactKey`
//     (MakeArtifactIdempotencyKey(hashutil.SHA256String)) is
//     the byte-stable production default the free function
//     delegates to. Removing the free function would force the
//     composition root to inject the derive into every caller
//     manually — a refactor that belongs in a separate cleanup
//     wave (out of Commit A scope).
//
// The deterministic byte format is unchanged across this audit-pin
// (the free function continues to delegate to defaultArtifactKey
// which delegates to the domain-owned SHA256String port default).
// godlike/06 SSOT
// discipline: the FUNCTION still produces the canonical output;
// the RECOMMENDED path forward is the factory.
//
// ArtifactIdempotencyKey computes the deterministic X-Idempotency-Key
// header value for a single artifact upload. The function is pure
// (no side effects, no timestamp / random / UUID inputs) so retries
// of the same (jobID, artifactID, sha256) triple collapse to the
// same remote-side dedup slot.
//
// Algorithm: SHA-256 of "jobID:artifactID:sha256Hex" (colon-separated
// concatenation), hex-encoded. The domain-owned hashutil.SHA256String
// implementation is the default leaf; callers may inject any compatible
// HashFunc through MakeArtifactIdempotencyKey.
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
	return defaultArtifactKey(jobID, artifactID, sha256Hex)
}

// ArtifactIdempotencyKeyFunc is the typed-closure returned by
// MakeArtifactIdempotencyKey. Callers store this as a struct field
// populated at construction time (godlike/06 dependency injection).
type ArtifactIdempotencyKeyFunc func(jobID, artifactID, sha256Hex string) string

// MakeArtifactIdempotencyKey is the canonical constructor binding a
// domain/remote/hashutil.HashFunc to the artifact-idempotency-key
// derivation. Returns a Pure + Deterministic + Header-safe closure.
//
// godlike/07 fail-closed: nil hash panics at construction time
// (composition root MUST inject a non-nil HashFunc,
// or a test fake for unit-test isolation per Commit D spec literal
// "Aggiungi un test unit con fake `HashFunc`").
func MakeArtifactIdempotencyKey(hash domainhashutil.HashFunc) ArtifactIdempotencyKeyFunc {
	if hash == nil {
		panic("remote.MakeArtifactIdempotencyKey: nil HashFunc — composition root must inject a HashFunc (godlike/07 fail-closed at construction)")
	}
	return func(jobID, artifactID, sha256Hex string) string {
		if jobID == "" || artifactID == "" || sha256Hex == "" {
			return ""
		}
		return hash(jobID + ":" + artifactID + ":" + sha256Hex)
	}
}

// defaultArtifactKey is the package-level production default bound to
// the domain-owned standard-library SHA-256 implementation. Initialised at
// package-load via MakeArtifactIdempotencyKey so the legacy free
// function ArtifactIdempotencyKey can delegate to it without per-call
// construction overhead.
//
// New code SHOULD prefer MakeArtifactIdempotencyKey + struct-field injection for
// explicit hash customisation (testable with fake HashFunc per the
// Commit D spec).
var defaultArtifactKey = MakeArtifactIdempotencyKey(domainhashutil.SHA256String)

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
