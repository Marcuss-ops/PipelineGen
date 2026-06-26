package qdrant

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// OutboxEventsDeadLetterAdapter bridges the DeadLetterChecker contract to
// the canonical outbox_events count via *outboxevents.Repository.
//
// QDRANT-003 (June 2026) closure — "Dead-letter reale" sub-task.
//
// Until this adapter shipped, cmd/admin/reindex_qdrant.go constructed the
// ReindexVerifier with `nil` for the DeadLetterChecker, which meant the
// Ready gate's `report.DeadLetterOpen == 0` condition was trivially
// satisfied — a flaky production with stuck dead-letter events could pass
// reindex-gate verification silently. Wiring the adapter here gives the
// verifier a real `SELECT COUNT(*) FROM outbox_events WHERE status =
// 'dead_letter'` signal that blocks the alias switch.
//
// LAYERING NOTE (June 2026): the FIRST cut of this adapter lived in
// internal/infrastructure/database/sqlite/assets/. That placement caused
// an import cycle:
//
//	qdrant -> application/scripts -> application/images
//	       -> application/assets/artifacts
//	       -> infrastructure/database/assetindex
//	       -> infrastructure/database/sqlite/assets
//	       -> infrastructure/qdrant       (cycle closes)
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
// implementation of DeadLetterChecker. nil repo is rejected — the verifier
// must never see a partially-constructed adapter in production.
func NewOutboxEventsDeadLetterAdapter(repo *outboxevents.Repository) *OutboxEventsDeadLetterAdapter {
	if repo == nil {
		panic("qdrant.NewOutboxEventsDeadLetterAdapter: repo is required")
	}
	return &OutboxEventsDeadLetterAdapter{repo: repo}
}

// CountOpen returns the count of outbox events currently in dead_letter
// status. Implements DeadLetterChecker.
//
// QDRANT-003 (June 2026): the verifier treats this as a HARD gate. If a
// single outbox event is stuck in dead_letter at switch time, the alias
// refuses to flip — a flaky producer must be visible to the operator
// before the new collection can serve read traffic.
func (a *OutboxEventsDeadLetterAdapter) CountOpen(ctx context.Context) (int, error) {
	if a == nil || a.repo == nil {
		return 0, nil
	}
	n, err := a.repo.CountByStatus(ctx, "dead_letter")
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// Compile-time assertion: the adapter statically satisfies the
// DeadLetterChecker contract. Drift is caught at build time.
var _ DeadLetterChecker = (*OutboxEventsDeadLetterAdapter)(nil)
