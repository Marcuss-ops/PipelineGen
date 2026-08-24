// dr/retention.go — QDRANT-005C PR3 RetentionService.
//
// QDRANT-005 closure (June 2026): the underlying cleanup command
// landed in qdrant.CollectionManager.CleanupWithConfig (QDRANT-005
// follow-up commit 07292503). This service is a THIN WRAPPER around
// CleanupWithConfig at the application layer, so:
//
//   - The admin CLI (cmd/admin/dr_qdrant.go) can sit at the
//     application-layer seam without dragging the admin path into
//     the infrastructure package.
//   - Tests substitute a stub RetentionExecutor to assert the flags
//     are passed through untransformed.
//   - The dr package stays cohesive: RestoreService + SnapshotService +
//     RetentionService all share the same ServiceDeps pattern.
//
// RetentionService.Apply takes the shared domain RetentionConfig alias.
// The RetentionExecutor port and infrastructure adapter therefore use the
// same Go type and the adapter delegates without conversion.
//
// Safe defaults enforced here (PR3 deviation from prior bool switch):
//   - retentionDays > 0 is the polygon OWNER for "are we allowed to
//     drop anything at all" — ≤0 is a hard no-op.
//   - KeepLastN floor of 2 (active + one rollback target) — matches
//     qdrant.CollectionManager.CleanupWithConfig's hard floor.
//   - ProtectedRollbackTarget is NEVER dropped (explicit pin per
//     operator intent).
//
// QDRANT-005C PR3 (June 2026): mirrors PR2 ServiceDeps struct pattern.
// Panics on nil Executor; Log falls back to zap.NewNop().
package dr

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// RetentionService applies the canonical QDRANT-005 retention pass.
// The active alias target is preserved; the protected rollback target
// (if supplied) is preserved; the keep_last_n floor (default 2)
// preserves the most-recent-N physical collections matching the
// schema prefix; the rest are dropped.
type RetentionService struct {
	cm  RetentionExecutor
	log *zap.Logger
}

// RetentionServiceDeps bundles the injectable ports.
//
//	Required (panic if nil): Executor
//	Optional (zero default): Log
type RetentionServiceDeps struct {
	Executor RetentionExecutor
	Log      *zap.Logger
}

// NewRetentionServiceFromDeps panics on nil Executor (the only
// required port). Log falls back to zap.NewNop().
func NewRetentionServiceFromDeps(deps RetentionServiceDeps) *RetentionService {
	if deps.Executor == nil {
		panic("dr.NewRetentionServiceFromDeps: RetentionServiceDeps.Executor must not be nil")
	}
	if deps.Log == nil {
		deps.Log = zap.NewNop()
	}
	return &RetentionService{cm: deps.Executor, log: deps.Log}
}

// Apply runs the canonical retention sweep with safe defaults strongly
// recomputed before delegation. The Executor (qdrant.CollectionManager,
// through RetentionExecutorAdapter) also enforces its own safe-floor,
// but re-applying here means a caller-supplied cfg with KeepLastN=0
// still results in a 2-floor pass.
func (s *RetentionService) Apply(ctx context.Context, cfg RetentionConfig) (*RetentionResult, error) {
	if cfg.RetentionDays <= 0 {
		return nil, fmt.Errorf("dr.RetentionService.Apply: RetentionDays must be > 0 (set to 1 for a keep_last_n-only sweep)")
	}
	if cfg.KeepLastN < 2 {
		cfg.KeepLastN = 2 // explicit floor to match CollectionManager's safety contract
	}

	res, err := s.cm.CleanupWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("dr.RetentionService.Apply: %w", err)
	}

	s.log.Info("retention applied",
		zap.Int("dropped", res.CollectionsDropped),
		zap.Int("kept", res.CollectionsKept),
		zap.Int("protected_kept", len(res.ProtectedKept)),
		zap.Strings("dropped_names", res.DroppedNames),
		zap.Int("retention_days", cfg.RetentionDays),
		zap.Int("keep_last_n", cfg.KeepLastN),
		zap.String("protected_rollback_target", cfg.ProtectedRollbackTarget))
	return res, nil
}
