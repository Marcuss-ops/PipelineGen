// Package idempotency is the canonical owner of the 3 idempotency
// key shapes that gate every duplicate-write surface in PipelineGen
// (Fase 5, July 2026).
//
// The 3 key shapes:
//
//	AssetKey(provider, clipID, sourceVersion, sha256File) string
//	  → "provider:clipID:sourceVersion:sha256File"
//	  For media_assets row identity (the UNIQUE column id).
//	  Guarantees: discovery dedup, scraper dedup, file-content dedup.
//
//	JobKey(provider, clipID, sourceVersion) string
//	  → "provider:clipID:sourceVersion"
//	  For outbox event_key (the UNIQUE column event_key).
//	  Guarantees: replay dedup, crash-recovery dedup.
//
//	OutboxKey(eventType, provider, clipID, sourceVersion) string
//	  → "eventType:provider:clipID:sourceVersion"
//	  For Qdrant upsert outbox event_key.
//	  Distinct from JobKey because the event_type disambiguates
//	  the dispatch target (asset.index.requested.v1 vs
//	  asset.drive.delete_requested.v1).
//
// # SEGMENT DELIMITER
//
// The 4-tuple is delimited by ':' (the codebase convention from
// existing key shapes — see BuildIndexEventKey in
// domain/asset/clip_identity.go which uses ':' as the segment
// separator; see also ArtifactIdempotencyKey in
// domain/remote/idempotency.go which uses ':' in the same way).
//
// The ErrInvalidSegment guard applies ONLY to the routing fields
// (eventType, provider) — the two segments that are part of the
// dispatch-routing identity and MUST be segment-count-stable so
// a downstream parser that splits on ':' (e.g. an outbox router
// that reads the provider segment) doesn't misparse. A provider
// like "art:list" or an event_type like "asset.index:requested"
// would otherwise silently produce a key with the wrong
// segment count.
//
// The guard does NOT apply to the data fields (clipID,
// sourceVersion, sha256File) — these are opaque identifiers
// that legitimately contain ':' as a SCHEME PREFIX, not a
// segment delimiter:
//   - sourceVersion often carries a 'sha256:' prefix
//     (e.g. "sha256:deadbeef..."), per the convention in
//     tests/e2e/qdrant_e2e_youtube_test.go::testSourceVersionFor
//   - clipID for the stock pipeline is "planner:<hash>:<index>"
//     (per internal/application/assets/providers/stock/.../
//     planner.go::buildClipPlan)
//   - sha256File may carry a "sha256:<hex>" prefix in some
//     upstream adapters
//
// Rejecting these inputs would be a production-silent bug —
// the canonical key constructor would fail-closed on inputs
// that the upstream pipeline legitimately produces. The
// data-field keys are opaque (no downstream parser splits on
// ':' to read clipID or sourceVersion) so the segment-count
// drift is benign.
//
// godlike/06 SSOT (Fase 5 user spec — Stabilisci la chiave
// canonica di idempotenza):
//
//	This package is the SOLE canonical owner of the 3 key
//	constructors. Other packages (artlist, outboxevents, drive,
//	clipindexer) MUST use these functions, not ad-hoc string
//	concatenations. A drift between the canonical key shape
//	and an ad-hoc producer surfaces as duplicate rows that the
//	UNIQUE constraint can't catch (the two producers hash to
//	different keys, so ON CONFLICT (event_key) DO NOTHING
//	doesn't fire).
//
// godlike/07 NO-FAKE-AVAILABILITY:
//
//	Every constructor rejects empty inputs with a typed error
//	(ErrEmptyProvider, ErrEmptyClipID, ErrEmptySourceVersion,
//	ErrEmptySHA256, ErrEmptyEventType, ErrInvalidSegment). The
//	dedup guarantees depend on the keys being COMPLETE — a
//	silently-hashed-empty input is the silent-loss anti-pattern
//	the gates exist to prevent. The user spec literal "Niente
//	dipendenza dal solo titolo" is enforced by the function
//	signature: none of the 3 constructors takes a title or
//	name parameter.
package idempotency

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Typed sentinels for the fail-closed guards. Callers branch
// on errors.Is to surface a typed diagnostic in operator logs.
// All sentinels carry the canonical "no fake availability"
// reason text for grep-ability.
var (
	// ErrEmptyProvider — provider is required for every key.
	// A missing provider is the canonical "wiring bug" case
	// (the caller forgot to thread the provider through).
	ErrEmptyProvider = errors.New("idempotency: provider is required (godlike/07 — no fake availability)")
	// ErrEmptyClipID — clip_id is required for every key.
	// A missing clip_id is the canonical "no fake identity"
	// case (the caller would otherwise hash a phantom row).
	ErrEmptyClipID = errors.New("idempotency: clip_id is required (godlike/07 — no fake availability)")
	// ErrEmptySourceVersion — source_version is required for
	// every key. A missing source_version is the canonical
	// "stale CAS fence" case (the worker didn't read the
	// current source_version before enqueueing).
	ErrEmptySourceVersion = errors.New("idempotency: source_version is required (godlike/07 — no fake availability)")
	// ErrEmptySHA256 — sha256(file) is required for AssetKey
	// (the row identity). A missing sha256 is the canonical
	// "download not yet complete" case — the caller should
	// use JobKey until the file is downloaded and the
	// sha256 is computed.
	ErrEmptySHA256 = errors.New("idempotency: sha256 file digest is required for AssetKey (use JobKey until the file is downloaded)")
	// ErrEmptyEventType — event_type is required for OutboxKey
	// (the dispatch target disambiguator).
	ErrEmptyEventType = errors.New("idempotency: event_type is required for OutboxKey (godlike/07 — no fake availability)")
	// ErrInvalidSegment — a ROUTING field (eventType, provider)
	// contains ':', the reserved segment delimiter. The
	// constructor rejects the input rather than silently
	// producing a mis-parsable key. The godlike/06 contract:
	// a downstream parser that splits on ':' (the canonical
	// segment delimiter) MUST see exactly 4/3/4 segments for
	// AssetKey/JobKey/OutboxKey. A provider like "art:list"
	// would otherwise silently produce a 5-segment key that
	// any ':'-splitter would misparse.
	//
	// NOTE: the guard applies ONLY to the routing fields. The
	// data fields (clipID, sourceVersion, sha256File) are opaque
	// and may legitimately contain ':' as a scheme prefix
	// (e.g. "sha256:abc", "planner:abc:0") — see the SEGMENT
	// DELIMITER section in the package doc.
	ErrInvalidSegment = errors.New("idempotency: ':' is reserved as the segment delimiter (godlike/06 — applies to ROUTING fields only; data fields clipID/source_version/sha256 may carry ':' as a scheme prefix)")
	// ErrInvalidRunForDedup — BuildKey's typed sentinel for
	// run-level dedup inputs (FASE 5 Commit B / Commit B
	// follow-up, July 2026). Triggered when the provider
	// discriminator is empty, when the canonical map is
	// empty (no segment values to hash), or when JSON
	// marshaling of the canonical map fails. Distinct from
	// ErrEmptyProvider/ErrInvalidSegment because the
	// run-level key shape is GENERAL (the canonical map
	// carries an arbitrary set of segments — provider +
	// term + folder + strategy + dry_run + limit, where
	// each caller picks its own canonical segment set).
	// The sentinel is per-provider-discovery so callers
	// outside the artlist package can rely on it for OTHER
	// run-level dedup keys (e.g. future stock-commodity
	// "stock-run" or "youtube-run" BuildKey callers).
	ErrInvalidRunForDedup = errors.New("idempotency: invalid run-level dedup input — provider must be non-empty, canonical map must carry at least one segment, and the canonical map must be JSON-marshalable (godlike/07 — no fake availability; see pkg/idempotency.BuildKey)")
)

// errInvalidSegment returns a per-field invalidSegmentError for
// the colon-collision guard on the routing fields (eventType,
// provider). The 3 callers (one per constructor) use the
// typed sentinel via errors.Is to branch on the failure mode.
func errInvalidSegment(field string) error {
	return &invalidSegmentError{field: field}
}

// invalidSegmentError wraps ErrInvalidSegment with a per-field
// diagnostic. errors.Is(invalidSegmentError, ErrInvalidSegment)
// is true so callers can branch on the typed sentinel; the
// Error() method adds the field name for operator triage.
type invalidSegmentError struct {
	field string
}

func (e *invalidSegmentError) Error() string {
	return "idempotency: '" + e.field + "' contains ':' (reserved segment delimiter; godlike/06 — fix the caller)"
}

func (e *invalidSegmentError) Is(target error) bool {
	return target == ErrInvalidSegment
}

// AssetKey constructs the canonical media_assets row identity.
// The 4-tuple (provider, clipID, sourceVersion, sha256File) is
// the user-spec literal from Fase 5 (July 2026):
//
//	"provider+clip_id+source_version+sha256(file) per gli asset"
//
// The 4-tuple is delimited by ':' (the codebase convention from
// existing key shapes — see BuildIndexEventKey in
// domain/asset/clip_identity.go which uses ':' as the segment
// separator; see also ArtifactIdempotencyKey in
// domain/remote/idempotency.go which uses ':' in the same way).
//
// Field rules (see the SEGMENT DELIMITER section in the package
// doc for the routing/data split rationale):
//   - provider  : ROUTING field — empty rejected with
//     ErrEmptyProvider; ':' rejected with
//     ErrInvalidSegment.
//   - clipID    : DATA field    — empty rejected with
//     ErrEmptyClipID; ':' ALLOWED (stock planner
//     IDs and sha256-prefixed IDs legitimately
//     use ':' as a scheme prefix).
//   - sourceVer : DATA field    — empty rejected with
//     ErrEmptySourceVersion; ':' ALLOWED (sha256
//     source_version is conventionally
//     "sha256:<hex>").
//   - sha256File: DATA field    — empty rejected with
//     ErrEmptySHA256; ':' ALLOWED.
//
// Empty inputs are rejected with a typed sentinel so the gates
// downstream (UNIQUE on media_assets.id, scraper dedup, etc.)
// cannot be silently bypassed by a half-wired caller. The
// checks are INLINED (not delegated to a helper) so the typed
// sentinel identity is preserved — errors.Is(returnedErr,
// ErrEmptyProvider) MUST be true per godlike/07.
//
// Invariant: JobKey(p, c, s) is a strict prefix of
// AssetKey(p, c, s, h) when the sha256 segment is appended.
// This lets a writer construct JobKey first (at job creation
// time, before the file is downloaded) and then upgrade to
// AssetKey (once the sha256 is computed) without changing
// the first 3 segments. The invariant is verified by
// TestJobKey_IsPrefixOfAssetKey in keys_test.go.
//
// godlike/07 NO-FAKE-AVAILABILITY: an empty sha256File is NOT
// a sentinel for "first attempt" or "not yet downloaded" —
// the asset row identity requires the file fingerprint.
// A caller that doesn't have the sha256 yet must use JobKey
// (the 3-tuple, no file hash) until the file is downloaded
// and the sha256 is computed. The writer rejects AssetKey
// calls with an empty sha256File.
func AssetKey(provider, clipID, sourceVersion, sha256File string) (string, error) {
	if provider == "" {
		return "", ErrEmptyProvider
	}
	if strings.Contains(provider, ":") {
		return "", errInvalidSegment("provider")
	}
	if clipID == "" {
		return "", ErrEmptyClipID
	}
	if sourceVersion == "" {
		return "", ErrEmptySourceVersion
	}
	if sha256File == "" {
		return "", ErrEmptySHA256
	}
	return provider + ":" + clipID + ":" + sourceVersion + ":" + sha256File, nil
}

// JobKey constructs the canonical outbox event_key for a job
// (provider+clip_id+source_version, no file hash — the file
// may not exist yet at job-creation time). The 3-tuple is the
// user-spec literal from Fase 5:
//
//	"provider+clip_id+source_version per i job/outbox"
//
// Field rules (see the SEGMENT DELIMITER section in the package
// doc for the routing/data split rationale):
//   - provider  : ROUTING field — empty rejected with
//     ErrEmptyProvider; ':' rejected with
//     ErrInvalidSegment.
//   - clipID    : DATA field    — empty rejected with
//     ErrEmptyClipID; ':' ALLOWED.
//   - sourceVer : DATA field    — empty rejected with
//     ErrEmptySourceVersion; ':' ALLOWED.
//
// Empty inputs are rejected with typed sentinels (same
// godlike/07 fail-closed discipline as AssetKey). The checks
// are INLINED so the typed sentinel identity is preserved.
//
// Use JobKey for: outbox events that don't have a file
// fingerprint yet (e.g. the discovery event at job creation,
// the scraper-candidate event before download). Use AssetKey
// for: media_assets row identity + outbox events that have
// already computed the file sha256.
func JobKey(provider, clipID, sourceVersion string) (string, error) {
	if provider == "" {
		return "", ErrEmptyProvider
	}
	if strings.Contains(provider, ":") {
		return "", errInvalidSegment("provider")
	}
	if clipID == "" {
		return "", ErrEmptyClipID
	}
	if sourceVersion == "" {
		return "", ErrEmptySourceVersion
	}
	return provider + ":" + clipID + ":" + sourceVersion, nil
}

// OutboxKey constructs the canonical outbox event_key for a
// Qdrant upsert event. The 4-tuple (eventType, provider,
// clipID, sourceVersion) extends JobKey with an event_type
// prefix so the dispatch target (e.g. asset.index.requested.v1
// vs asset.drive.delete_requested.v1) is part of the dedup
// identity. The event_type also provides SSOT surface for
// the dispatcher routing — one event_key per event_type per
// asset; the same asset can have multiple event_keys for
// different event_types in the outbox.
//
// Field rules (see the SEGMENT DELIMITER section in the package
// doc for the routing/data split rationale):
//   - eventType  : ROUTING field — empty rejected with
//     ErrEmptyEventType; ':' rejected with
//     ErrInvalidSegment (eventType is the
//     routing segment — must be segment-stable).
//   - provider   : ROUTING field — empty rejected; ':' rejected.
//   - clipID     : DATA field    — empty rejected; ':' ALLOWED.
//   - sourceVer  : DATA field    — empty rejected; ':' ALLOWED.
//
// Empty inputs are rejected with typed sentinels. The checks
// are INLINED so the typed sentinel identity is preserved.
//
// OutboxKey is the canonical surface for the "Qdrant upsert
// outbox key" mentioned in the user spec. The existing
// BuildIndexEventKey in domain/asset/clip_identity.go
// (which produces "index:assetID:sha256-hex16:...") is a
// specialized variant that adds model + version + collection
// segments; OutboxKey is the GENERAL form that
// BuildIndexEventKey will eventually route through (per the
// domain/asset/clip_identity.go comment that flags the
// infra-layer indexEventKey as a future refactor target).
func OutboxKey(eventType, provider, clipID, sourceVersion string) (string, error) {
	if eventType == "" {
		return "", ErrEmptyEventType
	}
	if strings.Contains(eventType, ":") {
		return "", errInvalidSegment("event_type")
	}
	if provider == "" {
		return "", ErrEmptyProvider
	}
	if strings.Contains(provider, ":") {
		return "", errInvalidSegment("provider")
	}
	if clipID == "" {
		return "", ErrEmptyClipID
	}
	if sourceVersion == "" {
		return "", ErrEmptySourceVersion
	}
	return eventType + ":" + provider + ":" + clipID + ":" + sourceVersion, nil
}

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
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%x", sum), nil
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
// pre-Commit-A surface: `internal/application/assets/providers/
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
	sum := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", sum), nil
}
