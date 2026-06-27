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
	"errors"

	"go.uber.org/zap"

	assets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
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
func NewPurgeService(repo *assets.ClipsRepository, log *zap.Logger) (*PurgeService, error) {
	if repo == nil {
		return nil, errors.New("admin.NewPurgeService: clips repository is required (admin tooling cannot bypass the canonical repo)")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &PurgeService{repo: repo, log: log}, nil
}

// HardDeleteClip physically removes a clip row + dependent rows.
// Bypasses the outbox (admin tooling runs offline). Idempotent:
// deleting a non-existent id returns nil (no rows affected).
func (s *PurgeService) HardDeleteClip(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("admin.HardDeleteClip: id is required")
	}
	s.log.Info("admin.HardDeleteClip: physical delete", zap.String("id", id))
	return s.repo.HardDelete(ctx, id)
}

// RestoreClip flips lifecycle_state back to 'ready'. Bypasses the
// outbox (admin tooling runs offline). Idempotent.
func (s *PurgeService) RestoreClip(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("admin.RestoreClip: id is required")
	}
	s.log.Info("admin.RestoreClip: lifecycle state -> ready", zap.String("id", id))
	return s.repo.Restore(ctx, id)
}
