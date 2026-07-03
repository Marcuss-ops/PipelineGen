// Package monitor — youtube discoveries ledger port.
package monitor

import (
	"context"

	assetsdb "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
)

// YoutubeDiscoveriesPort is the typed surface the channel monitor reads against the youtube_discoveries ledger.
type YoutubeDiscoveriesPort interface {
	TryReserve(ctx context.Context, channelID, videoID, policyVersion, sourceURL, title, discoveredAt string) (id string, won bool, attempt int, err error)
	MarkEnqueued(ctx context.Context, id, enqueuedAt string) error
	MarkRejected(ctx context.Context, id, rejectionReason string, retryable bool) error
	MaxDiscoveredAt(ctx context.Context, channelID string) (string, error)
	CommitEnqueueOutbox(ctx context.Context, discoveryID, enqueuedAt, idempotencyKey, payloadJSON string) error
	DrainPendingOutbox(ctx context.Context, limit int, leaseID, leaseUntil string) ([]assetsdb.OutboxEntry, error)
	DrainDispatched(ctx context.Context, limit int, leaseID, leaseUntil string) ([]assetsdb.OutboxEntry, error)
	MarkOutboxDispatched(ctx context.Context, outboxID int64, jobID string) error
	MarkOutboxFailed(ctx context.Context, outboxID int64, errMsg string) error
}
