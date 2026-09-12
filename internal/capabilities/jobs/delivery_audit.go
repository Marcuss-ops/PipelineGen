// Package jobs — delivery_audit.go: the delivery_log audit writer for
// the DeliveryHandler (extracted 2026-09-12 from delivery.go to keep the
// handler under max_lines_per_file_strict=600, godlike/08).
//
// Audit contract: delivery telemetry is written idempotently
// (ON CONFLICT DO UPDATE keyed by delivery_id = idempotency_key) so
// retries collapse onto one row. A failed audit write is surfaced to
// the caller — a success whose audit failed must not look identical
// to a recorded one (godlike/07 no-fake-availability).
package jobs

import (
	"context"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"go.uber.org/zap"
)

// recordDelivery writes (or updates) a delivery_log row keyed by
// idempotency_key (UNIQUE constraint). ON CONFLICT DO UPDATE collapses
// re-deliveries (e.g. after a 5xx retry) onto the same audit row. When
// db is nil the write is silently skipped so unit tests can construct
// the handler without a fixture.
func (h *DeliveryHandler) recordDelivery(ctx context.Context, req *deliveryRequest, statusCode int, responseHash, note string) error {
	if h.db == nil {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	// Use a detached context so the audit write is not cancelled when
	// the caller's HTTP request context expires. The write is
	// idempotent (ON CONFLICT DO UPDATE) so retries are safe.
	_, err := h.db.ExecContext(context.WithoutCancel(ctx), `
		INSERT INTO delivery_log (asset_id, endpoint_url, delivery_id, status_code, response_hash, delivered_at, created_at, note)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(delivery_id) DO UPDATE SET
		  status_code = excluded.status_code,
		  response_hash = excluded.response_hash,
		  delivered_at = excluded.delivered_at,
		  note = excluded.note
	`, req.Artifact.ArtifactID, req.Destination.DestinationID, req.IdempotencyKey, statusCode, responseHash, now, now, note)
	if err != nil {
		// Telemetry loss is audit data loss, not a routine hiccup: a webhook
		// delivery whose recording failed must not look identical to one that
		// was recorded. The log line carries enough context to reconstruct the
		// delivery from other sources (outbox row, receiver logs).
		h.log.Error("delivery_log insert failed (delivery telemetry lost — audit gap)",
			zap.String("idempotency_key", req.IdempotencyKey),
			zap.String("endpoint", req.Destination.DestinationID),
			zap.String("asset_id", req.Artifact.ArtifactID),
			zap.Int("status_code", statusCode),
			zap.String("note", note),
			zap.Error(err),
		)
		return err
	}
	return nil
}

// hashBody returns the lowercase hex SHA-256 of b. Empty input returns
// the SHA-256 of the empty string (a fixed constant), not "" — keeping

// the column stable for audits.
func hashBody(b []byte) string {
	return digest.SHA256Bytes(b)
}

// truncate returns at most n bytes of b. Used only for log lines; the
// delivery_log row stores the 1 MiB capped body hash.
func truncate(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return b[:n]
}
