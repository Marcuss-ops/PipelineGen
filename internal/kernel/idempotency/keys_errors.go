// Package idempotency — keys_errors.go
//
// Typed error sentinels and the colon-collision guard (errInvalidSegment,
// invalidSegmentError) used by the key constructors in keys.go and the
// hashing constructors in keys_hash.go. Split out of keys.go (refactor,
// August 2026) to keep each file focused; sentinel identity is unchanged.
package idempotency

import "errors"

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
	ErrInvalidRunForDedup = errors.New("idempotency: invalid run-level dedup input — provider must be non-empty, canonical map must carry at least one segment, and the canonical map must be JSON-marshalable (godlike/07 — no fake availability; see internal/kernel/idempotency.BuildKey)")
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
