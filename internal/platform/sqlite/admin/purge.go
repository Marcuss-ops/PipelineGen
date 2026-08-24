// Package admin (purge.go) — adapter that implements
// InternalAdminPurge by delegating to the lower-level mutation
// methods on *assets.ClipsRepository.
//
// This file lives alongside purge_ports.go in the same package on
// purpose: the interface declaration and the only legitimate
// implementation are co-located so a future engineer finding
// either artefact can read both in one screen.
//
// Production code MUST NOT import this package — see purge_ports.go
// package doc for the rationale. The CI lint
// scripts/ci-architectural-checks.sh bans `internal/application/**`
// and `internal/api/**` from importing this path.
package admin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"go.uber.org/zap"

	assets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
	txmutation "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesregistry"
)

// ErrAssetNotReadyForPurge is the canonical typed sentinel returned
// by PurgeService.HardDeleteClip when the row exists but the
// worker has not yet drained Qdrant for it. Bypassing the outbox
// is the explicit purpose of admin tooling, but admin-purge MUST
// refuse to physically delete a row whose Qdrant point is still
// present — that would orphan the vector and break the user's
// "no orphan vectors" invariant.
//
// Defensive contract: any new purge path that needs to bypass
// this check (e.g., a tool-tester re-delete) MUST override the
// gate explicitly with a typed error-wrap, not by mutating the
// sentinel. The sentinel is the operator-visible "you have a
// problem" surface — silent bypass is not acceptable.
var ErrAssetNotReadyForPurge = errors.New(
	"admin: asset not ready for physical purge (lifecycle_state must be 'DELETED' AND qdrant_point_state must be 'absent')",
)

// PurgeService implements InternalAdminPurge. Construction via
// NewPurgeService (returns a typed error when repo is nil — fail-fast
// per the AssetMutationPrimitives convention). The DB owner is the
// same *assets.ClipsRepository the dispatcher uses; the admin path
// only ever reads + writes via the lower-level mutation primitives
// (UpsertClip / Restore / HardDelete on ClipsRepository).
type PurgeService struct {
	repo *assets.ClipsRepository
	log  *zap.Logger
}

// Compile-time assertion: *PurgeService satisfies InternalAdminPurge.
// Per AGENTS.md Pattern 0, this lives at the adapter home so a port
// drift surfaces at compile time, not first invocation.
var _ InternalAdminPurge = (*PurgeService)(nil)

// NewPurgeService constructs the canonical adapter. Returns an error
// when repo is nil — fail-fast contract; callers (cmd/admin/*, the
// DR-drill script) propagate the error to the operator.
//
// Side effect (Wave 22 task 5 / PR-CLIP-RAW-MUTATIONS): the canonical
// txmutation package's SetLogger is wired HERE so the orphan-row WARN
// log line from sup() actually shows up on the operator console. Without
// this wire-up, txmutation would default to zap.NewNop() and the WARN
// would be silently swallowed (observable-but-silent = worst kind of
// logging). The named sub-logger (\"txmutation\") lets operators filter
// out the orphan-row warnings without losing the rest of admin's output.
func NewPurgeService(repo *assets.ClipsRepository, log *zap.Logger) (*PurgeService, error) {
	if repo == nil {
		return nil, errors.New("admin.NewPurgeService: clips repository is required (admin tooling cannot bypass the canonical repo)")
	}
	if log == nil {
		log = zap.NewNop()
	}
	// Wire txmutation package so HardDeleteTx orphan-row warnings surface.
	// Admin composition is the canonical observability-binding moment; we do
	// not restore any previously-installed txmutation logger (if one existed
	// before, the admin run supersedes it for the lifetime of this process).
	txmutation.SetLogger(log.Named("txmutation"))
	return &PurgeService{repo: repo, log: log}, nil
}

// HardDeleteClip physically removes a clip row + dependent rows.
// Bypasses the outbox (admin tooling runs offline). Idempotent:
// deleting a non-existent id returns nil (no rows affected).
//
// User-spec gate (QDRANT-asset-mutation isolation, June 2026):
// the row MUST have lifecycle_state='DELETED' AND
// qdrant_point_state='absent' before any physical delete fires.
// Otherwise the Qdrant vector would orphan — the user's "no
// orphan vectors" invariant. The gate is evaluated via a direct
// SQL lookup against the canonical media_assets table (the
// source of truth), not via the in-memory Asset struct (which
// does not yet carry qdrant_point_state — a deliberate
// minimum-blast-radius choice; the column is in schema via
// migration 095, the row read pins the contract).
func (s *PurgeService) HardDeleteClip(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("admin.HardDeleteClip: id is required")
	}

	var life, qps string
	err := s.repo.DB().QueryRowContext(ctx,
		`SELECT lifecycle_state, qdrant_point_state FROM media_assets WHERE id = ?`,
		id,
	).Scan(&life, &qps)
	if errors.Is(err, sql.ErrNoRows) {
		// Operator idempotency contract: deleting a non-existent
		// id returns nil (no rows affected). Re-run scripts after
		// re-failure MUST NOT fail loudly on already-deleted ids.
		s.log.Debug("admin.HardDeleteClip: missing row, idempotent no-op", zap.String("id", id))
		return nil
	}
	if err != nil {
		return fmt.Errorf("admin.HardDeleteClip: lookup %s: %w", id, err)
	}

	// Gate: only DELETED + absent pairs may proceed to physical
	// delete. The == comparisons are case-sensitive — lifecycle
	// canonical form is uppercase 'DELETED' (set by the worker
	// after the Qdrant drain); qdrant_point_state is lowercase
	// 'absent' (set by the worker post-deletion). Any deviation
	// means the worker has not yet finished — refuse.
	if life != "DELETED" || qps != "absent" {
		s.log.Info("admin.HardDeleteClip: refuse (worker not yet acked)",
			zap.String("id", id),
			zap.String("lifecycle_state", life),
			zap.String("qdrant_point_state", qps),
		)
		return fmt.Errorf("admin.HardDeleteClip %s: %w (lifecycle_state=%q, qdrant_point_state=%q)",
			id, ErrAssetNotReadyForPurge, life, qps)
	}

	s.log.Info("admin.HardDeleteClip: physical delete",
		zap.String("id", id),
		zap.String("lifecycle_state", life),
		zap.String("qdrant_point_state", qps),
	)
	// QDRANT-asset-mutation isolation (June 2026, PR-CLIP-RAW-MUTATIONS):
	// the legacy *assets.ClipsRepository.HardDelete is retired; the
	// physical-delete physics lives in txmutation.HardDeleteTx which
	// REQUIRES an open tx. We open the tx here in the gate function
	// and commit on success — this keeps the safety gate (above) and
	// the tx-scoped primitive (below) in the same code path so a
	// refactor that breaks one breaks both (fail-loud).
	tx, err := s.repo.DB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("admin.HardDeleteClip: begin tx %s: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := txmutation.HardDeleteTx(ctx, tx, id); err != nil {
		return fmt.Errorf("admin.HardDeleteClip: txmutation %s: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("admin.HardDeleteClip: commit %s: %w", id, err)
	}
	return nil
}

// RestoreClip flips lifecycle_state back to 'ready'. Bypasses the
// outbox (admin tooling runs offline). Idempotent.
func (s *PurgeService) RestoreClip(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("admin.RestoreClip: id is required")
	}
	s.log.Info("admin.RestoreClip: lifecycle state -> ready", zap.String("id", id))
	// QDRANT-asset-mutation isolation (June 2026, PR-CLIP-RAW-MUTATIONS):
	// legacy *assets.ClipsRepository.Restore is retired; delegate to
	// txmutation.RestoreTx with a caller-owned tx.
	tx, err := s.repo.DB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("admin.RestoreClip: begin tx %s: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := txmutation.RestoreTx(ctx, tx, id); err != nil {
		return fmt.Errorf("admin.RestoreClip: txmutation %s: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("admin.RestoreClip: commit %s: %w", id, err)
	}
	return nil
}
