// Package app — sourcing publisher + dispatcher adapters
// consolidated from youtube_publisher_adapter.go
// (PR-GODOBJ-Azione-4, July 2026).
//
// 2 adapters: sourcingPublisherAdapter, sourcingDispatcherAdapter.
package app

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
)

// ── sourcingPublisherAdapter ──────────────────────────────────────────

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

type sourcingDispatcherAdapter struct {
	disp *outbox.Dispatcher
}

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
