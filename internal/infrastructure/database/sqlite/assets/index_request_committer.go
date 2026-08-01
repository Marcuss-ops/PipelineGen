package assets

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/pkg/idempotency"
)

const indexRequestOperationUpsert = "UPSERT"

// IndexRequestOutbox is the narrow outbox write surface used by the canonical
// asset committer. Production uses *outboxevents.Repository; tests may provide
// a structural fake.
type IndexRequestOutbox interface {
	Enqueue(ctx context.Context, tx *sql.Tx, eventType, aggregateID, aggregateType, payloadJSON, eventKey string) (*outboxevents.EnqueueResult, error)
}

// priorityIndexRequestOutbox is the OPTIONAL priority-aware extension of
// IndexRequestOutbox. Production *outboxevents.Repository implements it
// (EnqueueWithPriority, migration 186); test fakes that only implement
// Enqueue keep working — CommitIndexRequestTx falls back to Enqueue when
// the optional method is absent. A script-required index request therefore
// jumps the outbox queue only when the backing repository supports
// priorities, never by silently dropping the event.
type priorityIndexRequestOutbox interface {
	EnqueueWithPriority(ctx context.Context, tx *sql.Tx, eventType, aggregateID, aggregateType, payloadJSON, eventKey string, priority int) (*outboxevents.EnqueueResult, error)
}

// IndexRequest describes one canonical indexing request emitted after the
// corresponding media_assets write has succeeded in the same transaction.
type IndexRequest struct {
	AssetID                  string
	Source                   string
	MediaType                string
	SourceVersion            string
	RequestedAt              time.Time
	UseProviderEventKey      bool
	IncludeSourceMetadata    bool
	IncludeEmbeddingMetadata bool
	// EventKeySuffix differentiates deterministic reindex requests that
	// share the same asset source_version but represent a changed
	// projection input, such as a repaired Drive location. It is appended
	// only to the idempotency key; source_version remains the canonical
	// asset fingerprint used by the supersede gate.
	EventKeySuffix string
	// Priority is the outbox scheduling priority (migration 186). 0 means
	// "use the default normal priority" (PriorityNormal). Producers whose
	// assets unblock script generation (stock pipeline finalizer) set
	// outboxevents.PriorityHigh so ClaimNext picks them before a
	// bulk-folder-sync backlog. Requires the backing repository to
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
// index-request event. Callers must first persist or update media_assets in tx;
// this function then builds the canonical envelope and inserts the outbox row
// using that same caller-owned transaction.
func CommitIndexRequestTx(
	ctx context.Context,
	tx *sql.Tx,
	box IndexRequestOutbox,
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

	if req.UseProviderEventKey {
		eventID = uuid.NewString()
		eventKey, err = idempotency.OutboxKey(
			outboxevents.EventAssetIndexRequested,
			req.Source,
			req.AssetID,
			req.SourceVersion,
		)
		if err != nil {
			return IndexRequestCommitResult{}, fmt.Errorf("asset committer: build outbox event_key: %w", err)
		}

		payloadMap := map[string]any{
			"schema_version":       outboxevents.ReindexEnvelopeV1Schema,
			"event_id":             eventID,
			"asset_id":             req.AssetID,
			"operation":            indexRequestOperationUpsert,
			"source_version":       req.SourceVersion,
			"target_index_version": clipindexer.CollectionVersion(),
			"requested_vectors":    []string{"text", "transcript"},
			"requested_at":         req.RequestedAt.UTC().Format(time.RFC3339Nano),
			"idempotency_key":      eventKey,
		}
		if req.IncludeSourceMetadata {
			payloadMap["source"] = req.Source
			payloadMap["media_type"] = req.MediaType
		}
		if req.IncludeEmbeddingMetadata {
			payloadMap["embedding_model"] = clipindexer.EmbeddingModel()
			payloadMap["embedding_version"] = clipindexer.EmbeddingModelVersion()
		}
		payload, err = json.Marshal(payloadMap)
		if err != nil {
			return IndexRequestCommitResult{}, fmt.Errorf("asset committer: build outbox payload: %w", err)
		}
	} else {
		var payloadString string
		eventKey, payloadString, err = outboxevents.BuildReindexEnvelopeV1(
			req.AssetID,
			clipindexer.CollectionVersion(),
			req.SourceVersion,
			req.RequestedAt,
		)
		if err != nil {
			return IndexRequestCommitResult{}, fmt.Errorf("asset committer: build outbox payload: %w", err)
		}
		payload = []byte(payloadString)
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
	// non-default priority AND the backing repository supports it.
	// Fallback to plain Enqueue keeps structural fakes (and repositories
	// without priority support) fully compatible.
	enqueue := func() (*outboxevents.EnqueueResult, error) {
		return box.Enqueue(
			ctx,
			tx,
			outboxevents.EventAssetIndexRequested,
			req.AssetID,
			"media_asset",
			string(payload),
			eventKey,
		)
	}
	if req.Priority > 0 {
		if prioBox, ok := box.(priorityIndexRequestOutbox); ok {
			enqueue = func() (*outboxevents.EnqueueResult, error) {
				return prioBox.EnqueueWithPriority(
					ctx,
					tx,
					outboxevents.EventAssetIndexRequested,
					req.AssetID,
					"media_asset",
					string(payload),
					eventKey,
					req.Priority,
				)
			}
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
