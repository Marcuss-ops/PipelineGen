// cmd/admin/identity_audit.go — the identity audit (item 12).
//
// The audit composes the two canonical identity invariants that must be ZERO
// before the Qdrant reindex:
//
//   - source identity (SQLite): a (source_type, source_ref) tuple resolving
//     to more than one canonical asset (media_asset_sources). Owned by
//     CanonicalIdentityResolver.AuditIdentity.
//   - point identity (Qdrant): a canonical asset appearing in more than one
//     Qdrant point (payload.asset_id). Owned by
//     verification.CountDuplicateAssetPoints.
//
// Both halves are merged into one capregistry.IdentityAuditReport. The command
// returns a non-nil error (and the admin binary exits non-zero) when either
// counter is non-zero, so it can gate scripts/CI. godlike/07 fail-closed: an
// unresolvable Qdrant collection or a scroll error aborts, never a partial
// "clean" verdict.
package audit
