// Package outbox — index_delete.go carries the consumer handler for
// `asset.index.delete_requested` events (QDRANT-002 PR2, closes ticket
// item H).
//
// The handler is the deletion counterpart of IndexingHandler:
//
//   - IndexingHandler ingests the asset → media_assets row + Qdrant upsert.
//   - IndexDeleteHandler removes the asset → Qdrant point delete +
//     media_assets row soft-delete.
//
// Idempotency is the primary contract:
//
//   - Qdrant's DeletePoints is natively idempotent (returns 200 with
//     deleted_count: 0 when the point is already absent; never returns
//     404 on a missing point).
//   - The pre-flight GetClip + lifecycle_state check short-circuits to
//     success when the asset row is already in a 'deleted' / 'DELETED'
//     state (matches the dual-casing comparison in
//     ClipsRepository.SoftDeleteFilter).
//
// State transition policy:
//
//   - Read asset by id; missing OR lifecycle_state in
//     {asset.StateDeleted, "deleted"} → treat as success (MarkCompleted).
//   - Otherwise: call Qdrant Deleter; non-nil error → return non-terminal
//     error → pool's MarkFailed backoff (eventually dead_letter).
//   - On Qdrant success: SoftDelete in SQLite → return nil.
//
// The canonical index_state machine (DISCOVERED → INDEX_PENDING → … →
// DELETE_PENDING → DELETED) lives in QDRANT-002 PR4. PR2 uses the
// existing SoftDelete path that sets lifecycle_state='deleted' (lowercase);
// both casings are recognised on read so future migration to the
// canonical 'DELETED' string is non-breaking.
package outbox

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// DeleteRequestSchemaVersion is the canonical, EXACT string the handler
// accepts. Producers MUST send "asset.index.delete_requested.v1"
// literally. Mismatch is TERMINAL — no retry — so producers upgrade
// instead of silently retrying on what looks like a routine failure.
const DeleteRequestSchemaVersion = "asset.index.delete_requested.v1"

// QdrantDeleter is the minimum surface the IndexDeleteHandler needs to
// remove Qdrant points. Declared locally so the handler does NOT import
// infrastructure/qdrant directly (mirrors the DeliveryHandler's
// *http.Client surface pattern).
//
// The production concrete is *qdrant.Service from
// internal/infrastructure/qdrant which satisfies this signature exactly.
type QdrantDeleter interface {
	DeletePoints(ctx context.Context, assetIDs []string) error
}

// AssetDeleter is the minimum surface for reading the current asset
// state (pre-flight idempotency check) and writing the soft-delete +
// canonical index_state transition (QDRANT-002 PR6). The production
// concrete is *assets.ClipsRepository from
// internal/infrastructure/database/sqlite/assets.
//
// SetIndexState writes the canonical index_state column on
// media_assets (the column added by migration 094). Called by
// IndexDeleteHandler.Handle between the idempotency pre-flight and
// the Qdrant delete (write DELETE_PENDING) and again after the SQLite
// SoftDelete (write DELETED). The method is intentionally narrow —
// a single column flip — so production wiring reflects an interface
// extension without an adapter layer. Tests pass a stub that
// records the (id, state) pairs so transitions are observable.
type AssetDeleter interface {
	GetClip(ctx context.Context, id string) (*asset.Asset, error)
	SoftDelete(ctx context.Context, id string) error
	SetIndexState(ctx context.Context, id string, state asset.IndexState) error
}

// indexDeleteRequestV1 is the canonical envelope for
// asset.index.delete_requested.v1 events.
//
// Required fields (handler fails-fast with TerminalError on missing-or-malformed):
//   - schema_version   (literal DeleteRequestSchemaVersion)
//   - event_id         (RFC4122 UUID or producer-chosen opaque token)
//   - asset_id         (canonical media_assets.id)
//   - idempotency_key  (operational audit + dedup at the outbox level)
//
// Optional:
//   - requested_at      (RFC3339 UTC; logged for audit only).
//
// Producers MUST NOT include embeddings, raw search vectors, or any
// payload that would make the event bloom to MBs. The handler
// reaches back into SQLite (asset_id lookup) to fetch current state
// — no need to ship embedding bytes in the wire envelope.
type indexDeleteRequestV1 struct {
	SchemaVersion  string `json:"schema_version"`
	EventID        string `json:"event_id"`
	AssetID        string `json:"asset_id"`
	RequestedAt    string `json:"requested_at,omitempty"`
	IdempotencyKey string `json:"idempotency_key"`
}

// IndexDeleteHandler is the real handler for asset.index.delete_requested.v1.
//
// Both QdrantDeleter and AssetDeleter are required for production wiring
// (BuildOutboxBundle populates them from *qdrant.Service and
// *assets.ClipsRepository respectively). Tests pass in-memory stubs that
// satisfy the local interfaces.
type IndexDeleteHandler struct {
	log           *zap.Logger
	qdrantDeleter QdrantDeleter
	assetDeleter  AssetDeleter
}

// NewIndexDeleteHandler wires the producer-side dependencies. log nil →
// nop logger. Either deleter nil is a programming error — the handler
// guards each call site with a nil-check so partial wiring degrades to
// "depend on the other" rather than crashing.
func NewIndexDeleteHandler(log *zap.Logger, qdrantDeleter QdrantDeleter, assetDeleter AssetDeleter) *IndexDeleteHandler {
	if log == nil {
		log = zap.NewNop()
	}
	return &IndexDeleteHandler{
		log:           log.Named("index_delete"),
		qdrantDeleter: qdrantDeleter,
		assetDeleter:  assetDeleter,
	}
}

// EventType returns the canonical outboxevents constant.
func (h *IndexDeleteHandler) EventType() string {
	return outboxevents.EventAssetIndexDeleteRequested
}

// Handle parses the v1 envelope, performs the idempotent delete
// (Qdrant + SQLite soft-delete), and returns nil on success. Validation
// failures and unsatisfiable payloads return typed terminal errors
// (PR1's outboxevents.NewTerminalError) so the pool's IsTerminal
// classifier dead-letters them immediately rather than burning
// max_attempts in a repair loop. Transient Qdrant failures return
// non-terminal errors so the pool retries per its backoff.
func (h *IndexDeleteHandler) Handle(ctx context.Context, evt outboxevents.Event) error {
	log := h.log
	if log == nil {
		log = zap.NewNop()
	}

	// Parse v1 envelope. Malformed JSON is TERMINAL — the producer
	// must fix the payload instead of retrying into a repair loop.
	var req indexDeleteRequestV1
	if err := json.Unmarshal([]byte(evt.PayloadJSON), &req); err != nil {
		log.Warn("asset.index.delete_requested payload parse failed (terminal)",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
			zap.Int("attempt", evt.AttemptCount),
			zap.Error(err),
		)
		return outboxevents.NewTerminalError(
			fmt.Errorf("asset.index.delete_requested payload parse: %w", err),
		)
	}

	// Strict envelope validation. Each missing/mismatched field is
	// TERMINAL — retrying won't bring the field into existence.
	if req.SchemaVersion != DeleteRequestSchemaVersion {
		log.Warn("asset.index.delete_requested schema_version mismatch (terminal)",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
			zap.String("got_version", req.SchemaVersion),
			zap.String("want_version", DeleteRequestSchemaVersion),
		)
		return outboxevents.NewTerminalError(fmt.Errorf(
			"asset.index.delete_requested: schema_version mismatch (terminal — got %q, want %q)",
			req.SchemaVersion, DeleteRequestSchemaVersion,
		))
	}
	if req.EventID == "" {
		log.Warn("asset.index.delete_requested: missing event_id (terminal)",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
		)
		return outboxevents.NewTerminalError(
			fmt.Errorf("asset.index.delete_requested: event_id is required (terminal)"),
		)
	}
	if req.AssetID == "" {
		log.Warn("asset.index.delete_requested: empty asset_id (terminal)",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
		)
		return outboxevents.NewTerminalError(
			fmt.Errorf("asset.index.delete_requested: empty asset_id (terminal — retry cannot conjure an id)"),
		)
	}
	if req.IdempotencyKey == "" {
		log.Warn("asset.index.delete_requested: missing idempotency_key (terminal)",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
		)
		return outboxevents.NewTerminalError(
			fmt.Errorf("asset.index.delete_requested: idempotency_key is required (terminal)"),
		)
	}

	reqLog := []zap.Field{
		zap.String("asset_id", req.AssetID),
		zap.Int64("event_id", evt.ID),
		zap.String("outbox_event_id", req.EventID),
		zap.String("idempotency_key", req.IdempotencyKey),
		zap.Int("attempt", evt.AttemptCount),
	}
	if req.RequestedAt != "" {
		reqLog = append(reqLog, zap.String("requested_at", req.RequestedAt))
	}

	// Idempotency pre-flight #1: asset row missing → already gone.
	// We use the asset deleter only when wired (production path is
	// always wired; tests may pass nil to exercise the no-asset-read
	// branch).
	if h.assetDeleter != nil {
		existing, err := h.assetDeleter.GetClip(ctx, req.AssetID)
		if err != nil {
			log.Warn("asset.index.delete_requested: GetClip failed (retryable)",
				append(reqLog, zap.Error(err))...,
			)
			return fmt.Errorf("asset.index.delete_requested GetClip(%s): %w", req.AssetID, err)
		}
		if existing == nil {
			log.Info("asset.index.delete_requested: asset row absent — treat as success (idempotent)",
				reqLog...,
			)
			return nil
		}
		// Idempotency pre-flight #2: lifecycle_state already in a
		// deleted state. Both casings match (canonical "DELETED" from
		// PR4 + legacy lowercase "deleted" from the existing SoftDelete
		// call). This is the same pair the SoftDeleteFilter accepts,
		// so future canonicalisation is non-breaking.
		switch string(existing.LifecycleState) {
		case "DELETED", "deleted":
			log.Info("asset.index.delete_requested: asset already in deleted state — treat as success (idempotent)",
				append(reqLog, zap.String("lifecycle_state", string(existing.LifecycleState)))...,
			)
			return nil
		}
	}

	// Mark delete-intent on media_assets.index_state BEFORE any
	// external side-effect (Qdrant delete) — QDRANT-002 PR6 column
	// promotion. DELETE_PENDING is observable from dashboards while
	// the SoftDelete is in flight; useful for an operator
	// investigating a stuck DELETE flow. Survives a worker crash
	// mid-flow: the outbox lease-fence retries the event from the
	// start (Qdrant delete is idempotent at the API layer). The
	// re-write of DELETE_PENDING on retry is itself idempotent
	// (column already holds DELETE_PENDING) so no transition noise.
	if h.assetDeleter != nil {
		if err := h.assetDeleter.SetIndexState(ctx, req.AssetID, asset.StateDeletePending); err != nil {
			// SetIndexState failure is retryable — same backoff path
			// as Qdrant errors. The retry re-runs SetIndexState →
			// Qdrant → SoftDelete → SetIndexState(DELETED). The only
			// cost is one redundant column flip on a retry; the
			// pre-flight's lifecycle_state=DELETED early-skip catches
			// the AFTER-SoftDelete case.
			log.Warn("asset.index.delete_requested: SetIndexState(DELETE_PENDING) failed (retryable)",
				append(reqLog, zap.Error(err))...,
			)
			return fmt.Errorf("asset.index.delete_requested SetIndexState(DELETE_PENDING, %s): %w", req.AssetID, err)
		}
	}

	// Qdrant delete first. DeletePoints is natively idempotent at
	// the API layer: a missing point returns 200 + deleted_count: 0,
	// not 404. We do NOT need a separate "is the point there?"
	// pre-flight — a second delete is a free no-op at the API.
	log.Info("asset.index.delete_requested: deleting Qdrant point", reqLog...)
	if h.qdrantDeleter != nil {
		if err := h.qdrantDeleter.DeletePoints(ctx, []string{req.AssetID}); err != nil {
			log.Warn("asset.index.delete_requested: Qdrant delete failed (retryable)",
				append(reqLog, zap.Error(err))...,
			)
			return fmt.Errorf("asset.index.delete_requested DeletePoints(%s): %w", req.AssetID, err)
		}
	}

	// SQLite soft-delete AFTER Qdrant success. Qdrant is the source
	// of truth for the vector index — only after it has been
	// acknowledged do we mark the asset as DELETED in SQLite so
	// future ingestions do not re-create a phantom point. If the
	// SoftDelete itself fails (rare; SQLite is local) the retry
	// path returns retryable so the worker re-runs both steps; the
	// Qdrant call is cheap to repeat (idempotent).
	if h.assetDeleter != nil {
		if err := h.assetDeleter.SoftDelete(ctx, req.AssetID); err != nil {
			log.Warn("asset.index.delete_requested: SQLite SoftDelete failed (retryable — Qdrant already done)",
				append(reqLog, zap.Error(err))...,
			)
			return fmt.Errorf("asset.index.delete_requested SoftDelete(%s): %w", req.AssetID, err)
		}
		// Final canonical state flip — index_state = 'DELETED' runs
		// AFTER SoftDelete so the column-side and lifecycle-side
		// tombstones settle in the same write batch. SQLite's per-row
		// write is implicit so a transient state where
		// lifecycle_state='deleted' AND index_state='DELETE_PENDING'
		// is briefly observable to a concurrent reader; the
		// idempotency pre-flight tolerates both (no false dead-letter
		// risk), and an operator dashboard sees a 1-row window on a
		// freshly-DELETED asset. The setIndexState failure here is
		// retryable: on the next lease the pre-flight catches
		// lifecycle_state='deleted' → early success; the wasted
		// transition is one column flip.
		if err := h.assetDeleter.SetIndexState(ctx, req.AssetID, asset.StateDELETED); err != nil {
			log.Warn("asset.index.delete_requested: SetIndexState(DELETED) failed (retryable)",
				append(reqLog, zap.Error(err))...,
			)
			return fmt.Errorf("asset.index.delete_requested SetIndexState(DELETED, %s): %w", req.AssetID, err)
		}
	}

	log.Info("asset.index.delete_requested: deletion complete", reqLog...)
	return nil
}
