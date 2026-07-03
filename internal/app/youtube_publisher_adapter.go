// Package app — sourcing publisher + dispatcher adapters extracted from
// assets_register_adapters.go (PR-GODOBJ-8, July 2026).
package app

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
)

// ── sourcingPublisherAdapter ──────────────────────────────────────────
// FASE 5 (June 2026): this adapter bridges the composition-root's
// delivery.Publisher (from DriveBundle.Publisher) into the sourcing
// layer so the YouTubeRegistrar can use the canonical Publisher path
// instead of direct DrivePort calls.

type sourcingPublisherAdapter struct {
	publisher delivery.Publisher
}

func (a *sourcingPublisherAdapter) Publish(ctx context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	if a.publisher == nil {
		return nil, fmt.Errorf("sourcingPublisherAdapter: publisher not wired")
	}
	return a.publisher.Publish(ctx, req)
}

// ── sourcingDispatcherAdapter ─────────────────────────────────────────
// Adapts outbox.Dispatcher to sourcing.IndexDispatcherPort.
// Converts sourcing.ExistingClip → asset.Asset before delegating to the dispatcher.
// Kept for legacy callers that still reference sourcing.IndexDispatcherPort
// directly (e.g. test fixtures and the queue-completion audit hook).

type sourcingDispatcherAdapter struct {
	disp *outbox.Dispatcher
}

// Compile-time assertion: sourcingDispatcherAdapter satisfies sourcing.IndexDispatcherPort.
var _ sourcing.IndexDispatcherPort = (*sourcingDispatcherAdapter)(nil)

func (a *sourcingDispatcherAdapter) EnqueueAndIndex(ctx context.Context, clip *sourcing.ExistingClip, contentHash string) error {
	if a.disp == nil {
		return nil
	}
	if clip == nil {
		return fmt.Errorf("sourcingDispatcherAdapter: clip is nil")
	}
	domainAsset := fromExistingClip(clip)
	return a.disp.EnqueueAndIndex(ctx, domainAsset, contentHash)
}
