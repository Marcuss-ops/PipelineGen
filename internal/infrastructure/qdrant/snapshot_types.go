// snapshot_types.go — marker file for the Qdrant snapshot types surface.
//
// PR3 mechanical split (June 2026): the user-stated file spec referenced
// "SnapshotConfig/SnapshotDescription" as the canonical snapshot-shape
// types. The Qdrant package has no SnapshotConfig, and SnapshotDescription
// already lives as a type alias in types_dr.go since QDRANT-005C PR3
// extracted the DR-family types to the canonical domain layer
// (internal/domain/qdrantdr/) with a public alias for callers that
// imported them from the qdrant package.
//
// The actual layout is:
//
//   - SnapshotDescription   →   type alias in types_dr.go
//     (resolves to qdrantdr.SnapshotDescription, the canonical shape).
//
//   - RetentionConfig + RetentionResult   →   same pattern: aliases in
//     types_dr.go pointing at the qdrantdr canonical shapes. These
//     were unified by PR-QDRANT-WIRE-MIRROR (June 2026) so the
//     application-layer dr package can use them without pulling in
//     the infra-side qdrant package.
//
//   - PointPayload   →   struct definition in types_dr.go (infra-only;
//     consumed by Client.OverwritePayload and reaper.Reaper.RedactPayload
//     in commit 07292503's reaper fork).
//
// PR3 preserves that placement rather than refolding the snapshot family
// back into a single snapshot_types.go because the QDRANT-005C PR3
// pre-split was a deliberate separation (the snapshot family is the
// only Client surface that fans into the dr.RestoreService; keeping
// it in types_dr.go preserves that consumer-proximity boundary
// without an extra cross-package import).
//
// RC reference (Qdrant spec) — handled by client_dr.go:
//
//	POST   /collections/{n}/snapshots              → CreateSnapshot
//	GET    /collections/{n}/snapshots              → ListSnapshots
//	GET    /collections/{n}/snapshots/{name}       → GetSnapshotURL
//	DELETE /collections/{n}/snapshots/{name}       → DeleteSnapshot
//	PUT    /collections/{n}/snapshots/recover      → RestoreSnapshot
//
// See types_dr.go for the type-alias declarations (SnapshotDescription)
// and the infra-only struct (PointPayload). See client_dr.go for the
// Method bodies and the QDRANT-005C PR3 invariants (verify-then-switch
// on RestoreSnapshot, idempotency on CreateSnapshot, etc.).
package qdrant
