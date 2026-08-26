package search

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// OutboxEventsDeadLetterAdapter bridges the schema.DeadLetterChecker contract to
// the canonical Qdrant-projection dead-letter count via *outboxevents.Repository.
//
// QDRANT-003 (June 2026) closure — "Dead-letter reale" sub-task.
//
// Until this adapter shipped, cmd/admin/reindex_qdrant.go constructed the
// ReindexVerifier with `nil` for the schema.DeadLetterChecker, which meant the
// Ready gate's `report.DeadLetterOpen == 0` condition was trivially
// satisfied. Wiring the adapter here gives the verifier a real signal for
// the indexing projection: `asset.index.requested` rows that are stuck in
// `dead_letter` still block alias promotion, while unrelated deletion flows
// are ignored by this gate.
//
// LAYERING NOTE (June 2026): the FIRST cut of this adapter lived in
// internal/platform/sqlite/assets/. That placement caused
// an import cycle:
//
//	qdrant -> application/scripts -> application/images
//	       -> application/assets/artifacts
//	       -> platform/sqlite/assetindex
//	       -> platform/sqlite/assets
//	       -> platform/qdrant       (cycle closes)
//
// The cycle is structural (qdrant already reaches assets via the
// application-level mediation path), so the asset package cannot import
// qdrant. Co-locating the adapter with the interface (qdrant package)
// avoids the cycle: qdrant -> outboxevents is a single direction with
// no path back through outbox (which is application-side).
type OutboxEventsDeadLetterAdapter struct {
	repo *outboxevents.Repository
}

// NewOutboxEventsDeadLetterAdapter wires a *outboxevents.Repository-backed
// implementation of schema.DeadLetterChecker. nil repo is rejected — the verifier
// must never see a partially-constructed adapter in production.
func NewOutboxEventsDeadLetterAdapter(repo *outboxevents.Repository) *OutboxEventsDeadLetterAdapter {
	if repo == nil {
		panic("qdrant.NewOutboxEventsDeadLetterAdapter: repo is required")
	}
	return &OutboxEventsDeadLetterAdapter{repo: repo}
}

// CountOpen returns the count of outbox events for the indexing projection
// that are currently in dead_letter status. Implements
// schema.DeadLetterChecker.
//
// QDRANT-003 (June 2026): the verifier treats this as a HARD gate for the
// indexing projection. If a single asset.index.requested event is stuck in
// dead_letter at switch time, the alias refuses to flip — a flaky producer
// must be visible to the operator before the new collection can serve read
// traffic. Deletion-lifecycle dead letters are intentionally out of scope
// for this reindex gate.
func (a *OutboxEventsDeadLetterAdapter) CountOpen(ctx context.Context) (int, error) {
	if a == nil || a.repo == nil {
		return 0, nil
	}
	n, err := a.repo.CountByEventTypeAndStatus(ctx, outboxevents.EventAssetIndexRequested, "dead_letter")
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// Compile-time assertion: the adapter statically satisfies the
// schema.DeadLetterChecker contract. Drift is caught at build time.
var _ schema.DeadLetterChecker = (*OutboxEventsDeadLetterAdapter)(nil)
