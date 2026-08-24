// Package enrichment — idempotency.go (PR-ENRICHMENT-IDEMPOTENCY-KEY, July 2026).
//
// EnrichmentIdempotencyKey is the deterministic SHA-256 triple-key
// helper for the stock RLM/LLM enrichment pass. The contract:
//
//	EnrichmentIdempotencyKey(chunkID, contentHash, version) (string, error)
//
// where the triple is:
//
//   - chunkID     = canonical media_assets.id (e.g. "stock:<run_fingerprint>:chunk:<i>")
//   - contentHash = SHA-256 hex of the chunk's video file (mirrors
//     media_assets.file_hash column; the 64-char lowercase
//     hex form is the canonical wire-shape)
//   - version     = EnrichmentVersion enum (V1 = 6 LLM-only fields per PR-011A;
//     V2+ will be added on future schema migrations)
//
// Property 1 — Deterministic: same triple -> same output byte-stable
//
//	across N invocations (idempotency-on-retry guarantee;
//	the AssetPublishedHandler's v1 envelope idempotency_key
//	field collapses at the outbox event_id UNIQUE constraint).
//
// Property 2 — Collision-resistant: different inputs -> different
//
//	output (birthday-paradox negligible at sha256-strength;
//	~10^6 stock chunks × 10 retries × 1000 resultHash variants
//	× N versions is well below collision).
//
// Property 3 — Header-safe: 64-char hex (no padding, no whitespace,
//
//	URL-safe character set; case-insensitive per RFC 7230 even
//	though the canonical recomputation always returns lowercase).
//
// Format: idempotency.BuildKeyString("stock-enrich", chunkID + ":" + contentHash + ":" + string(version))
// (colon-separated concatenation, mirroring the ArtifactIdempotencyKey
// and CompleteJobIdempotencyKey conventions in internal/domain/remote/).
// Commit A follow-up (July 2026): the canonical surface is now
// pkg/idempotency.BuildKeyString (delegated from this package),
// not a direct hashutil.SHA256String call. The byte-stable output
// is preserved across the migration.
//
// The ":version:" tail segment distinguishes v1 re-enrichment
// from a future v2 re-enrichment of the same chunk (so schema
// migrations produce different keys, which is the correct
// semantic — they are semantically different enrichments).
//
// godlike/06 SSOT (one canonical owner per fact): this is the
// single canonical idempotency-key construction for the
// stock RLM/LLM enrichment pass. The EnrichmentHandler
// (in this same package) is the SOLE producer; the
// AssetPublishedHandler (in internal/capabilities/jobs/outbox/
// asset_published.go) is the SOLE consumer. Both sides MUST
// recompute the key from the same triple; mismatched triples
// surface as ErrEnrichmentIdempotencyKeyConflict.
//
// godlike/06 SSOT (versioning): EnrichmentVersion is a string-typed
// enum with the V1 constant declared in this file. Future schema
// migrations (e.g. v2 with workspace-tags or signer-identity) MUST
// follow the godlike/07 EXPAND → BACKFILL → CUTOVER → CONTRACT
// sequence (introduce EnrichmentVersionV2 in EXPAND, mirror
// construction in BACKFILL, retire V1 in CUTOVER, CONTRACT in
// the final deprecation removal). The version is included in the
// idempotency_key derivation so a v1 re-enrichment and a v2
// re-enrichment of the same chunk produce different keys
// (semantically different enrichments).
//
// godlike/07 typed-error contract: EnrichmentIdempotencyKey returns
// ("", ErrEnrichmentIdempotencyKeyConflict) on ANY empty-input /
// malformed-input case. Callers MUST probe both the returned
// error (errors.Is) AND the returned key (IsValidEnrichmentIdempotencyKey)
// — the sentinel + the empty-key marker are TWO independent
// signals for the same wire-shape invariant.
package enrichment

import (
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/pkg/idempotency"
)

// EnrichmentVersion is the canonical schema-version enum for the
// stock RLM/LLM enrichment envelope. The string-typed form mirrors
// the codebase's existing string-enum pattern (e.g. job.StateMachine,
// delivery.DestinationKey) and is human-readable in audit logs.
//
// godlike/06 SSOT: EnrichmentVersion lives ONLY in this file. Future
// schema migrations (adding more fields to EnrichedFields) MUST
// add a new constant (e.g. EnrichmentVersionV2) and extend the
// IsValid() method — NOT redefine the type elsewhere.
type EnrichmentVersion string

// EnrichmentVersionV1 is the canonical V1 schema identifier. The
// V1 envelope covers the 6 LLM-only fields per PR-011A:
//
//	Category / Event / Round / Scene / Subject / Entities
//
// The literal "v1" is the wire-shape constant threaded through the
// idempotency_key derivation (so a v1 re-enrichment and a future
// v2 re-enrichment produce different keys).
const EnrichmentVersionV1 EnrichmentVersion = "v1"

// IsValid returns true if the version is a known canonical
// EnrichmentVersion constant. The validator is intentionally
// strict — unknown versions (e.g. "v3" added by a future PR
// before this file is updated) return false so the
// IdempotencyKey derivation surfaces ErrEnrichmentIdempotencyKeyConflict
// instead of silently producing a v1-shape key for a v3 envelope
// (which would mask the schema drift as a successful
// re-enrichment).
func (v EnrichmentVersion) IsValid() bool {
	switch v {
	case EnrichmentVersionV1:
		return true
	default:
		return false
	}
}

// EnrichmentIdempotencyKey computes the deterministic
// (chunkID, contentHash, version) idempotency-key triple for
// the stock RLM/LLM enrichment pass. The function is pure (no
// side effects, no timestamp / random / UUID inputs) so retries
// with the same triple collapse to the same outbox event_id
// slot (load-bearing for the AssetPublishedHandler's v1 envelope
// idempotency_key field).
//
// Algorithm: SHA-256 of "chunkID:contentHash:version"
// (colon-separated concatenation), hex-encoded.
// The hash is delegated to pkg/idempotency.BuildKeyString
// (Commit A follow-up, July 2026) — the canonical surface for
// run-level pre-joined key derivation. The provider discriminator
// "stock-enrich" asserts the canonical identity at the
// composition level; the byte-stable hash input is the EXACT
// pre-joined string the legacy helper produced, so in-flight
// outbox events queued under the legacy hash continue to MATCH
// at the kernel outbox event_id UNIQUE constraint across the
// migration.
//
// Stability contract: the algorithm byte-format is locked at
// PR-ENRICHMENT-IDEMPOTENCY-KEY (July 2026); future schema bumps
// (e.g. v2 with workspace-tags or signer-identity) require a
// 4-phase migration via the godlike/07 EXPAND → BACKFILL →
// CUTOVER → CONTRACT sequence (introduce EnrichmentVersionV2 in
// EXPAND, mirror construction in BACKFILL, retire V1 in CUTOVER,
// CONTRACT in the final deprecation removal).
//
// Empty-input edge case: an empty input triple would silently
// collapse ALL enrichments onto a single dedup slot (because
// idempotency.BuildKeyString("stock-enrich", "::") is a valid
// 64-char hex). Per
// godlike/07 no-fake-availability, we surface the empty-key
// marker (empty string) + ErrEnrichmentIdempotencyKeyConflict.
// Callers MUST check BOTH the returned error (errors.Is) AND
// the returned key (IsValidEnrichmentIdempotencyKey) — the
// sentinel + the empty-key marker are TWO independent signals
// for the same wire-shape invariant.
//
// Malformed contentHash edge case: a contentHash that is not
// 64-char lowercase hex (the canonical wire shape) is rejected
// with ErrEnrichmentIdempotencyKeyConflict. The validator is
// intentionally strict — accepting a non-canonical contentHash
// would risk a producer-side schema drift (e.g. uppercase hex
// or a truncated hash) being silently accepted as a valid
// idempotency_key input.
//
// Unknown version edge case: an EnrichmentVersion that does not
// satisfy IsValid() is rejected with ErrEnrichmentIdempotencyKeyConflict.
// This is the canonical "schema drift" signal — the producer
// (e.g. a future PR-011B/C that adds a v2 envelope) MUST update
// this file's EnrichmentVersion constants BEFORE shipping the
// producer-side change.
func EnrichmentIdempotencyKey(chunkID, contentHash string, version EnrichmentVersion) (string, error) {
	if chunkID == "" {
		return "", fmt.Errorf("%w: chunkID is empty (godlike/07 no-fake-availability)", ErrEnrichmentIdempotencyKeyConflict)
	}
	if !isValidHex64(contentHash) {
		return "", fmt.Errorf("%w: contentHash is not 64-char lowercase hex (godlike/07 no-fake-availability)", ErrEnrichmentIdempotencyKeyConflict)
	}
	if !version.IsValid() {
		return "", fmt.Errorf("%w: version=%q is not a known EnrichmentVersion (godlike/07 no-fake-availability — schema drift signal)", ErrEnrichmentIdempotencyKeyConflict, version)
	}
	// Commit A follow-up: delegate to pkg/idempotency.BuildKeyString
	// (byte-stable with legacy hashutil.SHA256String invocation
	// — see godlike/06 SSOT docstring at the top of this file).
	return idempotency.BuildKeyString("stock-enrich", chunkID+":"+contentHash+":"+string(version))
}

// IsValidEnrichmentIdempotencyKey returns true if `key` is a
// well-formed enrichment idempotency_key value: either the empty
// marker (64-char hex absent — callers must probe both this
// function AND the empty-case handler) or 64-char hex characters
// with case-insensitive A-F + 0-9.
//
// The case-insensitive allow (A-F AND a-f) matches the C6
// IsValidIdempotencyKey convention (RFC 7230 case-insensitive
// HTTP header value semantics) so the producer (enrichment
// handler) and consumer (AssetPublishedHandler) use the same
// validator. Canonical recomputation always returns lowercase.
func IsValidEnrichmentIdempotencyKey(key string) bool {
	if key == "" {
		// Empty marker is valid by definition; callers MUST
		// handle the "wiring-bug" case explicitly. See
		// EnrichmentIdempotencyKey docs for the empty triple
		// surface.
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

// ErrEnrichmentIdempotencyKeyConflict is the typed sentinel
// returned when the idempotency_key derivation observes a
// malformed triple. This is a corruption signal — the
// EnrichmentIdempotencyKey helper threads the same triple for
// retries so a divergent key indicates EITHER:
//
//   - an empty-input wiring bug (chunkID/contentHash/version
//     not populated by the caller)
//   - a producer-side schema drift (unknown version, e.g. "v3"
//     added by a future PR before this file is updated)
//   - a content-hash format drift (uppercase hex, truncated
//     hash, or non-canonical encoding)
//
// In all three cases, fail-closed: surface the sentinel so the
// caller can reject the call and re-baseline before retrying.
// Callers errors.Is against this sentinel.
//
// Note: this sentinel is DIFFERENT from the C6
// ErrArtifactIdempotencyKeyConflict and the C7
// ErrCompleteJobIdempotencyKeyConflict; the three surfaces
// ref different state machines (C6 artifacts vs C7 result rows
// vs C11 enrichment events) and conflating them would obscure
// the audit trail.
var ErrEnrichmentIdempotencyKeyConflict = errors.New("enrichment: idempotency key conflict (malformed triple: empty chunkID, non-canonical contentHash, or unknown version)")

// EnrichmentIdempotencyKeyDiagnostic is the canonical
// no-fake-availability diagnostic helper: returns a stable
// "what is wrong with this triple" message for the empty-input
// case. Used by adapters verifying the pre-emit gate (the
// EnrichmentHandler MUST call this BEFORE emitting the
// asset.published v1 outbox event so a malformed triple
// surfaces as a typed error, NOT a silent-success invalid
// key).
//
// Returns "" when the triple is valid (callers can use the
// empty-string return as a "no error" signal without
// probing EnrichmentIdempotencyKey's error return).
func EnrichmentIdempotencyKeyDiagnostic(chunkID, contentHash string, version EnrichmentVersion) string {
	if chunkID == "" {
		return "enrichment idempotency key: chunkID is empty (godlike/07 no-fake-availability)"
	}
	if !isValidHex64(contentHash) {
		return "enrichment idempotency key: contentHash is not 64-char lowercase hex (godlike/07 no-fake-availability)"
	}
	if !version.IsValid() {
		return fmt.Sprintf("enrichment idempotency key: version=%q is not a known EnrichmentVersion (godlike/07 no-fake-availability — schema drift signal)", version)
	}
	return ""
}

// isValidHex64 returns true if s is a 64-character lowercase
// hexadecimal string (the canonical contentHash wire shape).
// Uppercase hex is REJECTED — canonical contentHash from
// media_assets.file_hash is always lowercase. Callers that
// receive uppercase hex MUST lowercase before calling
// EnrichmentIdempotencyKey (the helper does NOT auto-lowercase
// to surface producer-side schema drift early).
func isValidHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
