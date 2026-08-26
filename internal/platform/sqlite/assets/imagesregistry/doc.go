// Package txmutation (Wave 22 task 5 / PR-CLIP-RAW-MUTATIONS, June 2026)
// implements the tx-scoped, restricted-surface low-level mutation
// primitives that previously lived as public methods on
// *assets.ClipsRepository (Restore, HardDelete, HardDeleteClip).
//
// Why this package exists (the "tx-scoped restricted restriction")
// ------------------------------------------------------------------
//
// Before PR-CLIP-RAW-MUTATIONS, *assets.ClipsRepository exposed three
// mutation methods that any production caller could invoke:
//
//   - UpsertClip(ctx, clip)        — remains public (//nolint:production
//     mark flags dispatcher-only path)
//   - Restore(ctx, id)             — REMOVED from *assets.ClipsRepository
//   - HardDelete(ctx, id)          — REMOVED from *assets.ClipsRepository
//   - HardDeleteClip(ctx, id)      — REMOVED from *assets.ClipsRepository
//
// The removal was forced by a real incident: a future caller could
// bypass the outbox (so the new read would not re-build the Qdrant
// vector during Restore) or orphan a Qdrant point (HardDelete that
// doesn't emit asset.index.delete_requested). Each of the three
// removed methods had at least one production-bypass hazard that the
// only legitimate caller (the admin tooling path) handles correctly
// because the admin path OWNS the gate + the tx + the audit log.
//
// This package is the canonical home for the new tx-scoped
// replacement surface:
//
//   - HardDeleteTx(ctx, tx, id) — physically removes the media_assets
//     row + dependent rows
//     (asset_locations, asset_processing,
//     asset_versions, asset_dedupe) inside
//     a caller-owned *sql.Tx. The admin
//     adapter (`internal/infrastructure/
//     database/sqlite/admin/purge.go`)
//     opens the tx, gate-checks
//     lifecycle_state='DELETED' AND
//     qdrant_point_state='absent', then
//     delegates here, then commits.
//
//   - RestoreTx(ctx, tx, id)   — flips lifecycle_state back to
//     'ready' inside a caller-owned *sql.Tx.
//     Idempotent. Caller-controlled rollback.
//
// //   - SetLogger(log)            — package-level logger hook so the
//
//	HardDeleteTx cascade DEBUG/INFO
//	log lines (probe no-op, per-table
//	child delete, parent delete) are
//	bound to a real *zap.Logger at the
//	choice of the composition root
//	(`admin.NewPurgeService` wires
//	log.Named("txmutation")). Default
//	before SetLogger invocation is
//	zap.NewNop() (silent under no-config).
//	Today the primitive emits no WARN:
//	the orphan-row detection contract is
//	owned by the caller
//	(`admin.PurgeService`), not the
//	primitive, by design (layered
//	responsibility, see §"Caller
//	contracts" §2).
//
// Caller contracts (MUST follow):
//
//  1. The caller MUST hold an open *sql.Tx. There is no
//     *sql.DB-bound version of these primitives by design.
//  2. The caller MUST pre-environment any safety gate that the
//     caller-spec requires (PR-CLIP-RAW-MUTATIONS places the gate
//     in admin.PurgeService NOT HERE so the gate is layered on top
//     of the primitive, not baked into it; the primitive is the
//     physics, the adapter is the gate — separated by design).
//  3. The caller is responsible for committing OR rolling back the
//     tx. Failure paths MUST call tx.Rollback() so that a partial
//     delete doesn't leak rows.
//
// Production-layer import discipline (enforced by
// scripts/ci-architectural-checks.sh Check 5):
//
//   - internal/application/**    — MUST NOT import this package
//     (uses outbox.Dispatcher, which routes via the safe path).
//   - internal/api/**             — MUST NOT import this package
//     (admin tooling runs offline via cmd/admin/**).
//   - internal/platform/sqlite/admin/ purge.go — the
//     only legitimate caller (QDRANT-asset-mutation isolation, June
//     2026).
//
// The CI lint ARCH-ALLOWLIST: admin-only comment marker is the
// explicit opt-in for any future admin migration / backfill file
// that legitimately needs an rg-gate exception. Today: zero
// allowlist markers in the production tree.
package imagesregistry

import ()
