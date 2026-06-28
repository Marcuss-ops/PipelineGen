// client_snapshots.go — marker file for the snapshot + restore surface.
//
// PR2 mechanical split (June 2026): the snapshot family
// (`CreateSnapshot`, `ListSnapshots`, `GetSnapshotURL`, `DeleteSnapshot`,
// `RestoreSnapshot`) lives in client_dr.go, which was extracted as
// part of QDRANT-005C PR3 (June 2026) — that earlier split predates
// PR2 by two months and the methods were never reintroduced into
// client.go. PR2 preserves that placement rather than refolding the
// snapshot family back into a single client_snapshots.go, because:
//
//   - The two files would have to be merged AND THEN re-split, which
//     produces an extra non-fast-forward on main for no functional
//     benefit (the methods stay on receiver *Client, in the same
//     package, with the same signature).
//   - client_dr.go also owns OverwritePayload (a /points/payload
//     merge helper used by reaper.Reaper.RedactPayload, shipped in
//     commit 07292503 as the upstream fix for the UpsertPoints-vec-null
//     reaper outage). That method is PR3-scope-adjacent (not a
//     designed DR surface) and coupling it with the snapshot family
//     keeps the reaper fork's compile-unblocker audit-trail intact.
//   - The snapshot family is the only Client surface that fans into
//     the dr.RestoreService (see internal/application/qdrant/dr/).
//     Keeping it in client_dr.go preserves that consumer-proximity
//     boundary without an extra cross-package import.
//
// RC reference (Qdrant spec) — handled by client_dr.go:
//
//	POST   /collections/{n}/snapshots              → CreateSnapshot
//	GET    /collections/{n}/snapshots              → ListSnapshots
//	GET    /collections/{n}/snapshots/{name}       → GetSnapshotURL
//	DELETE /collections/{n}/snapshots/{name}       → DeleteSnapshot
//	PUT    /collections/{n}/snapshots/recover      → RestoreSnapshot
//
// See client_dr.go for the method bodies and the QDRANT-005C PR3
// invariants (verify-then-switch on RestoreSnapshot, idempotency on
// CreateSnapshot, etc.).
package qdrant
