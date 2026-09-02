// Package media — index_request.go: the sole production emitter for the
// canonical asset.index.requested event on the PostgreSQL media adapter.
//
// Mirrors internal/platform/sqlite/assets/imagesregistry/
// index_request_committer.go statement-for-statement (godlike/06 SSOT: one
// envelope, two engine adapters). Callers must first persist or update
// media_assets in tx; this function then builds the canonical envelope and
// inserts the outbox row using that same caller-owned transaction.
package media

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	coreembedding "github.com/Marcuss-ops/PipelineGen/internal/kernel/embedding"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/idempotency"
)

// collectionVersion is the canonical index-schema version stamped in the
// reindex envelope. It mirrors clipindexer.collectionVersion ("v3", QDRANT-003);
// the parity suite asserts equality with clipindexer.CollectionVersion() so
// the two engine adapters can never drift apart.
const collectionVersion = "v3"

// IndexRequest describes one canonical indexing request emitted after the
// corresponding media_assets write has succeeded in the same transaction.
type IndexRequest struct {
	AssetID       string
	Source        string
	MediaType     string
	SourceVersion string
	RequestedAt   time.Time
	// EventKeySuffix differentiates deterministic reindex requests that
	// share the same asset source_version but represent a changed
	// projection input, such as a repaired Drive location. It is appended
	// only to the idempotency key; source_version remains the canonical
	// asset fingerprint used by the supersede gate.
	EventKeySuffix string
	// Priority is the outbox scheduling priority. 0 means "use the default
	// normal priority" (PriorityNormal). Requires the backing repository to
	// implement EnqueueWithPriority; otherwise the request is enqueued at
	// the default priority (never dropped).
	Priority int
}

// IndexRequestCommitResult reports the durable outbox write performed by
// CommitIndexRequestTx.
type IndexRequestCommitResult struct {
	EventID        string
	EventKey       string
	Inserted       bool
	ExistingStatus string
}

// CommitIndexRequestTx is the sole production emitter for the canonical
// index-request event on PostgreSQL. The payload map, event-key vector and
// envelope fields MUST stay byte-identical to the SQLite emitter so the
// outbox consumer cannot tell which engine produced the event.
func CommitIndexRequestTx(
	ctx context.Context,
	tx *sql.Tx,
	box outboxRepository,
	req IndexRequest,
) (IndexRequestCommitResult, error) {
	if tx == nil {
		return IndexRequestCommitResult{}, fmt.Errorf("asset committer: index request tx is required")
	}
	if box == nil {
		return IndexRequestCommitResult{}, fmt.Errorf("asset committer: index request outbox is required")
	}
	if req.AssetID == "" {
		return IndexRequestCommitResult{}, fmt.Errorf("asset committer: index request asset_id is required")
	}
	if req.SourceVersion == "" {
		return IndexRequestCommitResult{}, fmt.Errorf("asset committer: index request source_version is required")
	}
	if req.RequestedAt.IsZero() {
		req.RequestedAt = time.Now()
	}

	var (
		eventID  string
		eventKey string
		payload  []byte
		err      error
	)

	eventID = uuid.NewString()
	eventKey, err = idempotency.OutboxKey(
		EventAssetIndexRequested,
		req.Source,
		req.AssetID,
		req.SourceVersion,
	)
	if err != nil {
		return IndexRequestCommitResult{}, fmt.Errorf("asset committer: build outbox event_key: %w", err)
	}
	payloadMap := map[string]any{
		"schema_version":       ReindexEnvelopeV1Schema,
		"event_id":             eventID,
		"asset_id":             req.AssetID,
		"operation":            indexRequestOperationUpsert,
		"source_version":       req.SourceVersion,
		"index_revision":       req.SourceVersion,
		"target_index_version": collectionVersion,
		"requested_vectors":    []string{"text", "transcript"},
		"requested_at":         req.RequestedAt.UTC().Format(time.RFC3339Nano),
		"idempotency_key":      eventKey,
		"source":               req.Source,
		"media_type":           req.MediaType,
		"embedding_model":      coreembedding.ModelIDMultilingualE5,
		"embedding_version":    coreembedding.ModelRevisionMultilingualE5,
	}
	payload, err = json.Marshal(payloadMap)
	if err != nil {
		return IndexRequestCommitResult{}, fmt.Errorf("asset committer: build outbox payload: %w", err)
	}

	if suffix := strings.TrimSpace(req.EventKeySuffix); suffix != "" {
		eventKey += suffix
		var payloadMap map[string]any
		decoder := json.NewDecoder(bytes.NewReader(payload))
		decoder.UseNumber()
		if err := decoder.Decode(&payloadMap); err != nil {
			return IndexRequestCommitResult{}, fmt.Errorf("asset committer: decode outbox payload for suffix: %w", err)
		}
		payloadMap["idempotency_key"] = eventKey
		payload, err = json.Marshal(payloadMap)
		if err != nil {
			return IndexRequestCommitResult{}, fmt.Errorf("asset committer: encode outbox payload for suffix: %w", err)
		}
	}

	// Prefer the priority-aware enqueue when the producer requested a
	// non-default priority; fallback to plain Enqueue keeps structural
	// fakes (and repositories without priority support) fully compatible.
	enqueue := func() (*EnqueueResult, error) {
		return box.Enqueue(
			ctx,
			tx,
			EventAssetIndexRequested,
			req.AssetID,
			"media_asset",
			string(payload),
			eventKey,
		)
	}
	if req.Priority > 0 {
		enqueue = func() (*EnqueueResult, error) {
			return box.EnqueueWithPriority(
				ctx,
				tx,
				EventAssetIndexRequested,
				req.AssetID,
				"media_asset",
				string(payload),
				eventKey,
				req.Priority,
			)
		}
	}
	enqueueResult, err := enqueue()
	if err != nil {
		return IndexRequestCommitResult{}, fmt.Errorf("asset committer: enqueue outbox event: %w", err)
	}
	if enqueueResult == nil {
		return IndexRequestCommitResult{}, fmt.Errorf("asset committer: enqueue outbox event returned nil result")
	}

	return IndexRequestCommitResult{
		EventID:        eventID,
		EventKey:       eventKey,
		Inserted:       enqueueResult.Inserted,
		ExistingStatus: enqueueResult.ExistingStatus,
	}, nil
}
