// Package mediaregistry — identity_audit.go: the identity-audit contract
// (item 12 — the audit that must be clean before the Qdrant reindex).
//
// Two invariants gate the reindex. Both must be ZERO before the projection
// is rebuilt:
//
//   - DuplicateSourceIdentity: a (source_type, source_ref) tuple that
//     resolves to more than one canonical asset. The media_asset_sources
//     table is the canonical lookup (source_type | source_ref → asset_id);
//     a duplicate means two assets claim the same provider reference.
//   - DuplicateQdrantPoints: a canonical asset that appears in more than one
//     Qdrant point (payload.asset_id). The projection invariant is
//     1 canonical asset = 1 point; a duplicate means the point-ID / asset
//     relationship drifted.
//
// The two halves live in different layers (the source half is a SQLite
// registry fact, the Qdrant half is a projection fact) so implementations
// populate the half they own and the orchestrator merges them into one
// report. godlike/07 fail-closed: an audit counts, never guesses — any
// ambiguity stays visible as a non-zero counter.
package mediaregistry

import "context"

// IdentityAuditReport is the identity-audit outcome. Both counters must be
// zero before the Qdrant reindex.
type IdentityAuditReport struct {
	// DuplicateSourceIdentity is the number of (source_type, source_ref)
	// tuples resolving to more than one canonical asset
	// (media_asset_sources). Must be zero.
	DuplicateSourceIdentity int `json:"duplicate_source_identity"`

	// DuplicateQdrantPoints is the number of extra Qdrant points (beyond the
	// first) sharing the same canonical payload.asset_id. Must be zero.
	DuplicateQdrantPoints int `json:"duplicate_qdrant_points"`
}

// IdentityAuditor verifies the canonical identity invariants across the
// registry and its Qdrant projection. Implementations fill the half they
// own (SQLite source identity, Qdrant point identity); a caller composing
// both halves sums the counters into a single IdentityAuditReport.
type IdentityAuditor interface {
	// AuditIdentity returns the identity-audit counters for the owned half.
	AuditIdentity(ctx context.Context) (IdentityAuditReport, error)
}
