// Package sourcing — canonical typed-enum for the cross-package
// IndexingStatus surface (godlike/06 SSOT, §12-5 EXPAND).
//
// Authoritative source for the `indexing_status` JSON wire field across
// the entire PipelineGen stack. Production callers MUST consume this
// enum (or its `sourcing.IndexingStatus` application-layer alias) under
// the canonical 4-state lifecycle:
//
//	pending → completed
//	         ↘ failed   (terminal error; chunk retained on Drive)
//	         ↘ skipped  (asset retained without indexing — enrichment not configured)
//
// Pre-§12-5 EXPAND: the subtree used placeholder strings "enqueued" /
// "not_configured" via a `func IndexStatus(bool) string` helper, which
// was NOT a typed-enum. The §12-5 EXPAND phase retires the placeholder
// surface and decouples the canonical type from the boolean-derived
// string scheme (helper return type migrates from `string` to
// `SourcingIndexStatus`; wire value evolves from "enqueued"/"not_configured"
// to "pending"/"skipped" — documented in architecture/issues.yaml#
// PR-CROSSPACKAGE-INDEXING-STATUS-§12-5 and the commit body).
//
// godlike/06 SSOT: there is exactly ONE owner of the lifecycle enum and
// its wire serialization. The application-layer `sourcing.IndexingStatus`
// alias in internal/application/assets/sourcing/types.go is a TRANSPARENT
// Go type-alias — no parallel state, no forked serialization.
package sourcing

import (
	"bytes"
	"fmt"
)

// SourcingIndexStatus is the canonical 4-state lifecycle for the
// cross-package indexing stage.
//
// The underlying type is `string` so the default Go JSON marshaller
// already emits the canonical wire value (e.g. `"pending"`). The
// methods below exist to (a) validate on the way IN via UnmarshalJSON
// (per PR-STATUS-ENFORCE-TYPED — fail-closed for unknown bytes), and
// (b) lock the wire serialization against future drift (if anyone
// changes the underlying type from string to int64, custom MarshalJSON
// becomes load-bearing).
type SourcingIndexStatus string

// Canonical SourcingIndexStatus constants.
const (
	// SourcingIndexStatusPending — indexing has been enqueued but the
	// indexer has not yet committed the row (e.g. async Qdrant projection
	// still in flight).
	SourcingIndexStatusPending SourcingIndexStatus = "pending"

	// SourcingIndexStatusSkipped — the asset is retained but indexing
	// was bypassed (e.g. enrichment port not configured, asset-tree
	// downgraded to warn-only, or asset type is non-indexable). The
	// chunk is on Drive and queryable via non-Qdrant paths.
	SourcingIndexStatusSkipped SourcingIndexStatus = "skipped"

	// SourcingIndexStatusCompleted — terminal successful indexing state
	// (Qdrant upsert + AssetTree upsert + outbox event all succeeded).
	// Row is queryable end-to-end.
	SourcingIndexStatusCompleted SourcingIndexStatus = "completed"

	// SourcingIndexStatusFailed — terminal error state. Any of the 3
	// indexing sub-steps returned an unretriable error. The chunk is
	// still on Drive; operator backfill via POST /api/assets/operator/assets/:id/reindex.
	SourcingIndexStatusFailed SourcingIndexStatus = "failed"
)

// CanonicalSourcingIndexStatusValues enumerates all valid
// SourcingIndexStatus values, in canonical order. Useful for tests,
// CI-table rendering, and operators inspecting the lifecycle.
var CanonicalSourcingIndexStatusValues = []SourcingIndexStatus{
	SourcingIndexStatusPending,
	SourcingIndexStatusSkipped,
	SourcingIndexStatusCompleted,
	SourcingIndexStatusFailed,
}

// String returns the canonical persisted representation.
func (s SourcingIndexStatus) String() string {
	return string(s)
}

// IsValid reports whether the status matches one of the 4 canonical
// constants. Empty string is intentionally INVALID — a caller that
// wants to express "unset" MUST use SourcingIndexStatusSkipped
// explicitly (or refuse to emit a status at all).
func (s SourcingIndexStatus) IsValid() bool {
	switch s {
	case SourcingIndexStatusPending, SourcingIndexStatusSkipped,
		SourcingIndexStatusCompleted, SourcingIndexStatusFailed:
		return true
	}
	return false
}

// Validate returns a typed error if the status is not a canonical
// constant. The error message includes the offending bytes (per
// godlike/07 no-fake-availability) so operators can pin the wire
// emitter without referring back to documentation.
func (s SourcingIndexStatus) Validate() error {
	if !s.IsValid() {
		return fmt.Errorf("invalid SourcingIndexStatus: %q (canonical: pending, skipped, completed, failed)",
			string(s))
	}
	return nil
}

// MarshalJSON preserves the typed-enum wire shape (one of
// "pending"/"skipped"/"completed"/"failed"). The default Go marshaller
// would emit the underlying string anyway, but spelling the method
// explicitly locks the contract against future drift (e.g. someone
// changing the underlying type from `string` to `int64`).
//
// Invalid statuses return a non-nil error rather than silently emitting
// "" or "skipped" — per PR-STATUS-ENFORCE-TYPED the production wire
// boundary is fail-closed for unknown bytes.
func (s SourcingIndexStatus) MarshalJSON() ([]byte, error) {
	if !s.IsValid() {
		return nil, fmt.Errorf("cannot marshal invalid SourcingIndexStatus: %q (canonical: pending, skipped, completed, failed)",
			string(s))
	}
	return []byte(`"` + string(s) + `"`), nil
}

// UnmarshalJSON strictly validates the bytes-to-enum conversion. Any
// value not in the canonical 4-state set returns an error (no silent
// defaulting to "" or "skipped"). Per PR-STATUS-ENFORCE-TYPED, the
// production wire boundary is fail-closed for unknown bytes.
//
// Legacy placeholder strings ("enqueued", "not_configured") are
// rejected; production callers must migrate to the canonical wire
// values.
func (s *SourcingIndexStatus) UnmarshalJSON(b []byte) error {
	raw := bytes.Trim(b, `"`)
	parsed := SourcingIndexStatus(raw)
	if !parsed.IsValid() {
		return fmt.Errorf("invalid SourcingIndexStatus: %q (canonical: pending, skipped, completed, failed)", raw)
	}
	*s = parsed
	return nil
}

// ParseSourcingIndexStatusFromBytes is the canonical bytes-to-enum
// helper for non-JSON callers (e.g. SQLite row decoding via plain
// string columns). Returns the typed SourcingIndexStatus + a non-nil
// error on invalid bytes.
//
// Production code that consumes JSON wire payloads MUST prefer the
// UnmarshalJSON method (which delegates to IsValid). This helper is
// the canonical surface for non-JSON byte-slice reads.
func ParseSourcingIndexStatusFromBytes(b []byte) (SourcingIndexStatus, error) {
	parsed := SourcingIndexStatus(b)
	if !parsed.IsValid() {
		return "", fmt.Errorf("invalid SourcingIndexStatus: %q (canonical: pending, skipped, completed, failed)", b)
	}
	return parsed, nil
}
