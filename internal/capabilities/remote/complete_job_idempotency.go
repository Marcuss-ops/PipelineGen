// Package remote — complete_job_idempotency.go (P0 Commit 7, July 2026).
//
// CompleteJobIdempotencyKey is the deterministic SHA-256 triple-key
// helper for the (jobID, attempt, resultHash) idempotency surface on
// the Sender-side atomic CompleteJob. Mirrors the C6 ArtifactIdempotencyKey
// property set:
//
//	CompleteJobIdempotencyKey(jobID, attempt, resultHash string) string
//
// Property 1 — Deterministic: same inputs -> same output byte-stable
// across N invocations (idempotency-on-retry guarantee; the UNIQUE
// constraint on job_results(job_id, attempt, result_hash) collapses
// at the SQLite level).
//
// Property 2 — Collision-resistant: different inputs -> different
// output (birthday-paradox negligible at sha256-strength; ~10^6 jobs
// × 10 attempts × 10^6 resultHash variants is well below collision).
//
// Property 3 — Header-safe + row-safe: 64-char hex (no padding, no
// whitespace, URL-safe character set; case-insensitive per RFC 7230
// even though SQLite column is case-sensitive — the canonical
// recomputation always returns lowercase).
//
// Format: digest.SHA256String("jobID:" + attempt + ":" + resultHash)
// (colon-separated concatenation). The ":attempt:" middle segment
// distinguishes attempts with the same jobID + resultHash from
// each other (so retry attempt N+1 cannot collide with attempt N
// even if the result payload is intentionally identical).
//
// godlike/06 SSOT: this is the single canonical idempotency-key
// construction for the Sender-side atomic-complete surface. The
// Sender-side CompleteJob adapter MUST recompute the key from the
// same triple on every replay; mismatched triples surface as
// ErrCompleteJobIdempotencyKeyConflict (typed sentinel, godlike/07).
package remote

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// DEPRECATED (Commit A / FASE 5 follow-up, July 2026):
//
// godlike/06 SSOT — this free function is DEPRIORITIZED in favor of
// the typed-factory pattern. Future production callers SHOULD
// migrate to:
//
//	derive := MakeCompleteJobIdempotencyKey(digest.HashFunc)
//	service := &CompletionService{derive: derive, ...} // field injection
//
// (composition root wires the derive using digest.HashFunc
// or a test fake per the Commit D spec literal "Aggiungi un test
// unit con fake `HashFunc`").
//
// The free function remains in the surface AS-IS for two reasons:
//
//  1. Back-compat: the existing production callers (the
//     completion publisher + tx_outbox + helpers) thread
//     this free function for the legacy complete-job-key
//     derivation. Removing the free function in Commit A
//     would force ALL callers to migrate in a single PR —
//     godlike/06 minimum-blast-radius forbids this. The
//     DEPRECATED note directs future migrations.
//
//  2. Default-priming: the package-init
//     `defaultCompleteJobKey` (MakeCompleteJobIdempotencyKey
//     (digest.SHA256String)) is the byte-stable
//     production default the free function delegates to.
//     Removing the free function would force the composition
//     root to inject the derive into every caller manually —
//     a refactor that belongs in a separate cleanup wave
//     (out of Commit A scope).
//
// The deterministic byte format is unchanged across this
// audit-pin (the free function continues to delegate to
// defaultCompleteJobKey which delegates to
// the domain-owned SHA256String default). godlike/06 SSOT discipline: the
// FUNCTION still produces the canonical output; the
// RECOMMENDED path forward is the factory.
//
// Mirrors the ArtifactIdempotencyKey audit-pin in
// internal/domain/remote/idempotency.go (Commit A follow-up
// applies the FASE 5/idempotency audit-pin discipline uniformly
// across the legacy free-function surface).
//
// CompleteJobIdempotencyKey computes the deterministic
// (jobID, attempt, resultHash) idempotency-key triple for a Sender-
// side CompleteJob call. The function is pure (no side effects, no
// timestamp / random / UUID inputs) so retries with the same triple
// collapse to the same job_results row via the UNIQUE constraint +
// ON CONFLICT DO NOTHING pattern.
//
// Algorithm: SHA-256 of "jobID:attempt:resultHash" (colon-separated
// concatenation), hex-encoded. The domain-owned digest.SHA256String
// implementation is the default leaf; callers may inject any compatible
// HashFunc through MakeCompleteJobIdempotencyKey.
//
// Stability contract: the algorithm byte-format is locked at
// P0 §7 (July 2026); future schema bumps (e.g. v2 with worker-id
// or signer-identity) require a 4-phase migration via the
// godlike/07 EXPAND → BACKFILL → CUTOVER → CONTRACT sequence
// (introduce CompleteJobIdempotencyKeyV2 in EXPAND, mirror
// construction in BACKFILL, retire V1 in CUTOVER, CONTRACT in
// the final deprecation removal).
//
// Empty-input edge case: an empty input triple would silently
// collide with another empty-triple call (because
// digest.SHA256String("::") is a valid 64-char hex). Per
// godlike/07 no-fake-availability, we surface the empty-key
// marker (empty string) instead. Callers MUST check
// IsValidCompleteJobIdempotencyKey(key) AND handle the empty case
// as a "wiring-bug" signal (not a wire-shape concern).
//
// attempt < 0 is treated like an empty triple: an attempt counter
// that has been corrupted to a negative value is a typed
// configuration bug, and surface-fail-closed is the canonical
// godlike/07 posture. attempt == 0 is the canonical valid
// first-attempt value.
func CompleteJobIdempotencyKey(jobID string, attempt int, resultHash string) string {
	if jobID == "" || resultHash == "" || attempt < 0 {
		return ""
	}
	return defaultCompleteJobKey(jobID, attempt, resultHash)
}

// CompleteJobIdempotencyKeyFunc is the typed-closure returned by
// MakeCompleteJobIdempotencyKey. Callers store this as a struct
// field populated at construction time (godlike/06 dependency injection).
type CompleteJobIdempotencyKeyFunc func(jobID string, attempt int, resultHash string) string

// MakeCompleteJobIdempotencyKey is the canonical constructor for
// the (jobID, attempt, resultHash) triple-key derivation. Mirrors
// the C6 MakeArtifactIdempotencyKey shape: panic-on-nil HashFunc at
// construction (godlike/07 fail-closed); callers receive the
// HashFunc via constructor injection.
func MakeCompleteJobIdempotencyKey(hash digest.HashFunc) CompleteJobIdempotencyKeyFunc {
	if hash == nil {
		panic("MakeCompleteJobIdempotencyKey: nil HashFunc — composition root must inject a HashFunc (godlike/07 fail-closed at construction)")
	}
	return func(jobID string, attempt int, resultHash string) string {
		if jobID == "" || resultHash == "" || attempt < 0 {
			return ""
		}
		return hash(jobID + ":" + strconv.Itoa(attempt) + ":" + resultHash)
	}
}

// defaultCompleteJobKey is the package-level production default
// bound to the domain-owned standard-library SHA-256 implementation. Mirrors
// defaultArtifactKey for the (jobID, attempt, resultHash) surface.
var defaultCompleteJobKey = MakeCompleteJobIdempotencyKey(digest.SHA256String)

// IsValidCompleteJobIdempotencyKey returns true if `key` is a
// well-formed key for the job_results UNIQUE column: either the
// empty marker (64-char hex absent — callers must probe both this
// function AND the empty-case handler) or 64-char hex characters
// with case-insensitive A-F + 0-9.
//
// The case-insensitive allow (A-F AND a-f) matches the C6
// IsValidIdempotencyKey convention so both Send/Receive sides use
// the same validator. Canonical recomputation always returns
// lowercase but a Sender-side rewriter could uppercase the key
// before INSERT.
func IsValidCompleteJobIdempotencyKey(key string) bool {
	if key == "" {
		// Empty marker is valid by definition; callers MUST handle
		// the "wiring-bug" case explicitly. See CompleteJobIdempotencyKey
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

// ErrCompleteJobIdempotencyKeyConflict is the typed sentinel returned
// when the Sender-side CompleteJob observes a replayed triple with a
// DIFFERENT result_hash than the original (jobID, attempt) slot. This
// is a corruption signal — the Sender-side adapter threads the same
// resultHash for retries so a divergent hash indicates EITHER:
//
//   - a network bug that mangled the payload
//   - a Sender-side caller bug that re-derived the key incorrectly
//   - the wrong result was attached to the wrong (jobID, attempt) slot
//
// In all three cases, fail-closed: surface the sentinel so the
// caller can reject the call and re-baseline before retrying.
// Callers errors.Is against this sentinel.
//
// Note: this sentinel DIFFERENT from the artefacts'
// ErrArtifactIdempotencyKeyConflict; the two surfaces ref different
// state machines (C6 artifacts vs C7 result rows) and conflating
// them would obscure the audit trail.
var ErrCompleteJobIdempotencyKeyConflict = errors.New("complete job: idempotency key conflict (replay with different result_hash)")

// CompleteJobIdempotencyKeyDiagnostic is the canonical
// no-fake-availability diagnostic helper: returns a stable
// "what is wrong with this triple" message for the empty-input
// case. Used by adapters verifying the pre-TX refutation gate.
func CompleteJobIdempotencyKeyDiagnostic(jobID string, attempt int, resultHash string) string {
	if jobID == "" {
		return "complete job idempotency key: jobID is empty (godlike/07 no-fake-availability)"
	}
	if attempt < 0 {
		return fmt.Sprintf("complete job idempotency key: attempt=%d is negative (godlike/07 no-fake-availability)", attempt)
	}
	if resultHash == "" {
		return "complete job idempotency key: resultHash is empty (godlike/07 no-fake-availability)"
	}
	return ""
}
