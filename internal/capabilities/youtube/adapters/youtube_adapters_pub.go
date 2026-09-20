// Package app — sourcing publisher adapter
// consolidated from youtube_publisher_adapter.go
// (PR-GODOBJ-Azione-4, July 2026).
//
// 1 adapter: SourcingPublisherAdapter.
//
// SourcingDispatcherAdapter was DELETED here on 2026-09-20. sourcing.
// IndexDispatcherPort still has a live consumer (sourcing/youtube/service.go's
// indexDisp field) but its canonical implementer is
// ytadapters.YoutubeIndexDispatcherAdapter, pinned in the composition root at
// internal/app/wiring/assets_register_adapters.go:37. Two adapters satisfied the
// same port and only one was ever constructed, so this one's EnqueueAndIndex was
// unreachable.
package adapters

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
)

// ── SourcingPublisherAdapter ──────────────────────────────────────────

type SourcingPublisherAdapter struct {
	publisher delivery.Publisher
}

func (a *SourcingPublisherAdapter) Publish(ctx context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	if a.publisher == nil {
		return nil, fmt.Errorf("SourcingPublisherAdapter: publisher not wired")
	}
	return a.publisher.Publish(ctx, req)
}

// ── SourcingDispatcherAdapter ─────────────────────────────────────────
//
// Deleted on 2026-09-20; see the package header for why.
