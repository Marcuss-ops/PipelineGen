// Package idempotency — keys_hash.go
//
// Run-level dedup key hashing (BuildKey, BuildKeyString). Split out of
// keys.go (refactor, August 2026).
package idempotency

import (
	"encoding/json"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/pkg/digest"
)

// BuildKey constructs a run-level dedup key from a provider-type
// discriminator + a canonical map of segments (FASE 5 Commit B
// follow-up, July 2026). The canonical map shape is GENERAL — each
// caller (artlist.RunDedupKey, future stock.RunDedupKey, future
// youtube.RunDedupKey) builds its own canonical segment set
// (term/folder_id/strategy/dry_run/limit for artlist; etc.).
//
// Return shape: a 64-character lowercase SHA-256 hex string of the
// canonical map's JSON-marshaled bytes. This matches the bytes
// produced by the legacy artlist.runDedupKey private helper (commit
// 9 ship-gate trajectory) so in-flight jobs already queued with the
// legacy hash key will MATCH across the migration — godlike/06 SSOT
// keeps the storage-layer UNIQUE constraint on `jobs.active_key`
// byte-stable.
//
// Why JSON marshaling for the canonical map (not ':'-delimited
// concatenation like AssetKey/JobKey/OutboxKey):
//   - The canonical map shape is GENERAL: callers pick the segment
//     set per provider-type. Adopting the AssetKey/JobKey-style
//     would force a 1:1 positional-string API that pins the segment
//     list at the package level — the opposite of "GENERAL
//     canonical-map" contract.
//   - JSON produces a deterministic byte sequence for a given
//     map[string]any content (Go's encoding/json sorts map keys
//     alphabetically — verified by Go stdlib spec). The SHA-256
//     hash on top of that byte sequence is byte-stable across
//     call sites and across runs.
//   - The legacy artlist.runDedupKey used the EXACT same
//     canonical-map + json.Marshal + sha256 pipeline; migrating
//     to BuildKey preserves the legacy byte-stable output.
//
// Fail-closed guards (godlike/07 — no fake availability):
//   - provider == ""                          → ErrInvalidRunForDedup
//   - len(canonical) == 0                     → ErrInvalidRunForDedup
//   - strings.Contains(provider, ":")        → ErrInvalidSegment
//     (a provider discriminator like "art:list-run" would be
//     ambiguous with the ':' segment delimiter used by the
//     3 canonical positional constructors; reject it)
//   - json.Marshal(canonical) != nil          → ErrInvalidRunForDedup
//     (the legacy runDedupKey had a fmt.Sprintf fallback for this
//     case; the new BuildKey fails closed per godlike/07 — a
//     non-marshalable canonical is a programming error in the
//     caller, not a transient UX situation)
//
// godlike/06 SSOT rationale: BuildKey is the SINGLE canonical
// surface for run-level dedup keys (provider-type discriminator +
// canonical map → SHA-256 hex). Per-provider packages (artlist,
// future stock, future youtube) MUST delegate to BuildKey via
// `idempotency.BuildKey("<provider>-run", canonical)`. Ad-hoc
// concatenation in the caller package would defeat the byte-stable
// cross-package unification that lets the kernel job broker's
// UNIQUE on `jobs.active_key` collapse distinct operator requests
// across entry points (handler enqueue + orchestrator
// DiscoverAndQueueRun). The legacy artlist.runDedupKey is REMOVED
// in Commit B.
//
// godlike/07 NO-FAKE-AVAILABILITY: every validation step rejects
// potentially-fake inputs with a typed sentinel. The sentinel
// hierarchy is:
//
//	ErrInvalidRunForDedup   → the higher-level sentinel for the
//	                         run-level input surface.
//	ErrInvalidSegment       → re-used from the 3 positional
//	                         constructors; a provider
//	                         discriminator containing ':' is
//	                         structurally ambiguous (segment
//	                         delimiter collision).
//
// Both satisfy errors.Is dispatch so a caller can branch on either
// level of the chain.
func BuildKey(provider string, canonical map[string]any) (string, error) {
	if provider == "" {
		return "", ErrInvalidRunForDedup
	}
	if strings.Contains(provider, ":") {
		// Same segment-collision guard as the positional
		// constructors; a provider like "art:list-run" would
		// silently produce a 2-prefix-segment key that any
		// future ':'-splitter would misparse. The guard is
		// strictly about provider DISCRIMINATOR stability, not
		// data field stability — the positional AssetKey/JobKey/
		// OutboxKey constructors exempt data fields from this
		// guard (e.g. "sha256:abc" is allowed as a data field);
		// BuildKey's provider parameter IS a routing field, so
		// the guard applies.
		return "", errInvalidSegment("provider")
	}
	if len(canonical) == 0 {
		return "", ErrInvalidRunForDedup
	}
	// encoding/json sorts map keys alphabetically (Go stdlib
	// contract for map[string]any). The sorted JSON bytes are
	// deterministic across runs — the SHA-256 on top is
	// byte-stable across processes.
	raw, err := json.Marshal(canonical)
	if err != nil {
		// A non-marshalable canonical map is the canonical
		// "caller produced an unrepresentable value" case
		// (e.g. a func or chan inside the map). godlike/07
		// forbids silent fallback — the caller MUST fix the
		// canonical shape. The legacy runDedupKey had a
		// fmt.Sprintf fallback path; BuildKey fails closed.
		return "", ErrInvalidRunForDedup
	}
	return digest.SHA256Bytes(raw), nil
}

// BuildKeyString constructs a run-level dedup key from a
// provider-type discriminator + a pre-joined raw byte sequence
// (Commit A follow-up, July 2026).
//
// This is the BYTE-STABLE delegation surface for callers whose
// canonical content is already a pre-joined byte sequence — i.e.,
// the caller has assembled the join shape inline (e.g.
// `chunkID + ":" + contentHash + ":" + string(version)`) and
// needs the SAME byte-stable hash output the legacy
// `hashutil.SHA256String(joined)` invocation produced. Typical
// pre-Commit-A surface: `internal/capabilities/assets/providers/
// stock/enrichment/idempotency.go::EnrichmentIdempotencyKey`
// (the bespoke stock RLM/LLM enrichment key constructor). After
// migration, the caller delegates to BuildKeyString instead of
// calling `hashutil.SHA256String` directly — godlike/06 SSOT
// (one canonical owner for run-level key hashing), with byte-
// stability preserved across the migration (in-flight outbox
// events queued under the legacy hash continue to MATCH at the
// kernel outbox event_id UNIQUE constraint).
//
// Difference from BuildKey (Commit B): BuildKey takes a
// canonical map[string]any and JSON-marshals it before hashing
// (general shape for callers that build their canonical as a
// map). BuildKeyString takes the EXACT bytes the caller wants
// hashed (verbatim path for callers whose canonical IS a
// pre-joined string). Both produce a 64-char lowercase SHA-256
// hex; the bytes fed into SHA-256 are the only difference
// (json.Marshal(canonical) vs []byte(raw)).
//
// Provider validation: identical to BuildKey (empty → fail,
// ':' in → ErrInvalidSegment via the per-field wrapper that
// errors.Is-dispatches to ErrInvalidSegment).
//
// Raw validation: empty raw → ErrInvalidRunForDedup (a
// pre-joined-but-empty byte sequence is the canonical "the
// caller produced a structurally invalid join" wire-shape
// signal — operators grep on the empty-marker surface to find
// upstream wiring bugs that pre-empt an outbox-key collision).
//
// godlike/06 SSOT rationale: per-package run-level key
// constructors (artlist.RunDedupKey, stock.EnrichmentIdempotencyKey,
// future youtube.RunDedupKey, etc.) MUST delegate to one of:
//   - BuildKey (canonical-map form, JSON-marshaled bytes)
//   - BuildKeyString (pre-joined string form, verbatim bytes)
//
// Ad-hoc `hashutil.SHA256String(joined)` calls outside this
// package are the canonical godlike/06 SSOT violation that
// Commit A closes for the stock enrichment path. Future
// youtube.RunDedupKey should prefer BuildKey over BuildKeyString
// (canonical-map form is more general) unless there's a
// similar byte-stability requirement in flight.
//
// godlike/07 typed-error contract: every validation step
// returns a typed sentinel that satisfies errors.Is dispatch.
// callers branch on errors.Is for fail-closed error handling.
func BuildKeyString(provider, raw string) (string, error) {
	if provider == "" {
		return "", ErrInvalidRunForDedup
	}
	if strings.Contains(provider, ":") {
		// Same segment-collision guard as BuildKey (and the
		// positional constructors). Provider is a routing field
		// — ':' in the discriminator is structural ambiguity.
		return "", errInvalidSegment("provider")
	}
	if raw == "" {
		return "", ErrInvalidRunForDedup
	}
	return digest.SHA256String(raw), nil
}
