// Package monitor — youtube discoveries ledger port.
//
// FASE 3.7 Commit 1b (2026-07-04): DrainPendingOutbox + DrainDispatched
// return `[]monitor.OutboxEntry` (monitor-owned projection; struct
// defined in types_dto.go) instead of the infra's row-as-scanned
// projection (the `OutboxEntry` declared in the SQLite-side
// `monitor_outbox.go`). The composition root
// (`internal/app/lifecycle.go::monitorDiscoveriesAdapter`) translates
// at wire-up time, so the application-layer orchestration only sees
// the monitor-canonical types. Zero infra import in this file.
package assets

import (
	"context"
)

// YoutubeDiscoveriesPort is the typed surface the channel monitor reads against the youtube_discoveries ledger.
type YoutubeDiscoveriesPort interface {
	TryReserve(ctx context.Context, channelID, videoID, policyVersion, sourceURL, title, discoveredAt string) (id string, won bool, attempt int, err error)
	MarkEnqueued(ctx context.Context, id, enqueuedAt string) error
	MarkRejected(ctx context.Context, id, rejectionReason string, retryable bool) error
	MaxDiscoveredAt(ctx context.Context, channelID string) (string, error)
	CommitEnqueueOutbox(ctx context.Context, discoveryID, enqueuedAt, idempotencyKey, payloadJSON string) error
	DrainPendingOutbox(ctx context.Context, limit int, leaseID, leaseUntil string) ([]OutboxEntry, error)
	DrainDispatched(ctx context.Context, limit int, leaseID, leaseUntil string) ([]OutboxEntry, error)
	MarkOutboxDispatched(ctx context.Context, outboxID int64, jobID string) error
	MarkOutboxFailed(ctx context.Context, outboxID int64, errMsg string) error
}
