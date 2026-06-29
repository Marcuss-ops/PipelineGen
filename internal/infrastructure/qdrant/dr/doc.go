// Package dr is the qdrant capability slice for disaster recovery (snapshots,
// restore, dr adapter reconciliation, retention config bridge to qdrantdr).
//
// EXPAND Tier-1 scaffolding (June 2026): empty sub-package established as the
// canonical home for dr-side files migrated in the subsequent BACKFILL phase.
// Per godlike/07 §migration sequence, content migration is gated and shipped
// as a follow-up commit. The wire-mirror pattern documented in
// architecture/deprecations.yaml::PR-QDRANT-WIRE-MIRROR (record #9) references
// this sub-package as the BACKFILL destination for SnapshotDescription,
// RetentionConfig, RetentionResult type aliases currently living in types_dr.go.
package dr
