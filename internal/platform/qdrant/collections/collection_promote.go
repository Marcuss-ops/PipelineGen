package collections

import (
	"context"
	"fmt"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
)

// ── Promote + Reindex ──────────────────────────────────────────────────

// PromoteCandidate is a compatibility wrapper for callers migrating from
// the pre-state-machine API. It never mutates Qdrant directly: READY builds
// are activated through ActivateProjection, while an already ACTIVE build is
// a successful idempotent no-op that only refreshes dependent caches.
func (cm *CollectionManager) PromoteCandidate(ctx context.Context, candidate string) error {
	if !cm.consumeVerified(candidate) {
		return fmt.Errorf("%w: candidate %q (call VerifyCandidate first)", ErrPromoteWithoutVerify, candidate)
	}
	projectionID, ok := cm.projectionByCollection(candidate)
	if !ok {
		return fmt.Errorf("%w: candidate %q is not registered with Projection Manager", ErrProjectionNotFound, candidate)
	}
	projection, _ := cm.Projection(projectionID)
	if projection.Status == string(capregistry.ProjectionActive) {
		if cm.OnAliasSwitch != nil {
			cm.OnAliasSwitch()
		}
		return nil
	}
	if projection.Status != string(capregistry.ProjectionReady) {
		return fmt.Errorf("%w: candidate %q is %s", ErrProjectionNotReady, candidate, projection.Status)
	}
	return cm.ActivateProjection(ctx, projectionID, projection.SourceRegistrySeq)
}

// ReindexCandidate checks whether the candidate collection has points.
// When PointTotal is zero the collection was created by PrepareCandidate
// but never backfilled — returns transport.ErrReindexRequired to block promotion.
// The operator must run `go run ./cmd/admin reindex-qdrant --apply` to
// backfill data before the server can start with a ready Qdrant index.
func (cm *CollectionManager) ReindexCandidate(ctx context.Context, candidate string) error {
	info, err := cm.client.GetCollection(ctx, candidate)
	if err != nil {
		return fmt.Errorf("reindex candidate: get collection %q: %w", candidate, err)
	}
	if info.PointTotal == 0 {
		cm.log.Warn("collection has zero points — reindex required before promotion",
			zap.String("candidate", candidate))
		return &transport.ErrReindexRequired{Collection: candidate}
	}
	cm.log.Debug("reindex candidate: collection has points, proceeding",
		zap.String("candidate", candidate),
		zap.Int("points", info.PointTotal))
	return nil
}
