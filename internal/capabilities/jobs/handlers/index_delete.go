// Package outbox — index_delete.go carries the consumer handler for
// `asset.index.delete_requested` events (VECTOR-002 PR2, closes ticket
// item H).
//
// The handler is the deletion counterpart of IndexingHandler:
//
//   - IndexingHandler ingests the asset → media_assets row + pgvector upsert.
//   - IndexDeleteHandler removes the asset → pgvector point delete +
//     media_assets row soft-delete.
//
// Idempotency is the primary contract:
//
//   - pgvector's DeletePoints is natively idempotent (returns 200 with
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
//   - Otherwise: call pgvector Deleter; non-nil error → return non-terminal
//     error → pool's MarkFailed backoff (eventually dead_letter).
//   - On pgvector success: SoftDelete in SQLite → return nil.
//
// The canonical index_state machine (DISCOVERED → INDEX_PENDING → … →
// DELETE_PENDING → DELETED) lives in VECTOR-002 PR4. PR2 uses the
// existing SoftDelete path that sets lifecycle_state='deleted' (lowercase);
// both casings are recognised on read so future migration to the
// canonical 'DELETED' string is non-breaking.
package handlers

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/event"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// errIndexLifecycleTerminal is the typed-terminal error sentinel
// raised on envelope failures. The wrap path stamps every string
// with a recognisable prefix so the production pool's
// IsTerminal classifier (outboxevents.NewTerminalError consumer)
// dead-letters immediately rather than spinning through
// max_attempts. Every Handle-returned terminal error wraps this
// via outboxevents.NewTerminalError.
//
// Blocco 3.1 commit 2/3 invariant: any return path that detects a
// malformed envelope, schema mismatch, missing field, or
// lifecycle-state inconsistency (Drive-block guard) MUST route
// through this sentinel + NewTerminalError wrap. Pragmatic
// envelope-validation loops (e.g. "missing lifecycle_state hop")
// belong here, NOT in handler-side transient error returns.
var errIndexLifecycleTerminal = errors.New("index_delete: terminal envelope error")

// DeleteRequestSchemaVersion is the canonical, EXACT string the handler
// accepts. Producers MUST send "asset.index.delete_requested.v1"
// literally. Mismatch is TERMINAL — no retry — so producers upgrade
// instead of silently retrying on what looks like a routine failure.
const DeleteRequestSchemaVersion = event.AssetIndexDeleteRequestedV1Schema

// VectorPointDeleter is satisfied by *pgvector.IndexWriter (see the
// compile-time assertion at
// internal/platform/postgres/media/index_writer.go). The interface
// itself lives in ports.go per AGENTS.md Pattern 0 — application-layer
// consumers MUST NOT redeclare it locally. PR 4 (refactor/single-pgvector-runtime)
// consolidated the previous pair of duplicated `VectorPointDeleter`
// declarations (one in infra/pgvector/types.go and one local here)
// into this single port.
//
// Production concrete: *pgvector.IndexWriter from
// internal/platform/postgres/media which satisfies the signature exactly.

// AssetDeleter is the minimum surface for reading the current asset
// state (pre-flight idempotency check) and writing the soft-delete +
// canonical index_state transition (VECTOR-002 PR6). The production
// concrete is *assets.ClipsRepository from
// internal/platform/sqlite/assets.
//
// SetIndexState writes the canonical index_state column on
// media_assets (the column added by migration 094). Called by
// IndexDeleteHandler.Handle between the idempotency pre-flight and
// the pgvector delete (write DELETE_PENDING) and again after the SQLite
// SoftDelete (write DELETED). The method is intentionally narrow —
// a single column flip — so production wiring reflects an interface
// extension without an adapter layer. Tests pass a stub that
// records the (id, state) pairs so transitions are observable.
type AssetDeleter interface {
	GetClip(ctx context.Context, id string) (*asset.Asset, error)
	SoftDelete(ctx context.Context, id string) error
	SetIndexState(ctx context.Context, id string, state asset.IndexState) error
	// SetLifecycleState writes the Blocco 3.1 canonical
	// media_assets.lifecycle_state column (5-state deletion
	// machine ACTIVE → DELETE_REQUESTED → DRIVE_DELETE_PENDING →
	// INDEX_DELETE_PENDING → DELETED). The IndexDeleteHandler
	// calls this method ONLY on the terminal hop — after pgvector
	// delete + SQLite SoftDelete + index_state=DELETED — to close
	// the state machine so an operator dashboard reads
	// 'lifecycle_state=DELETED' (not stuck at 'INDEX_DELETE_PENDING')
	// for a fully retired asset. Production concrete
	// *assets.ClipsRepository satisfies it directly (added in
	// Blocco 3.1 commit 2/3 at
	// internal/platform/sqlite/assets/clips_lifecycle_state.go).
	// Pattern 0 narrowness: ONLY SetLifecycleState — not the
	// broader CRUD port — so IndexDeleteHandler cannot accidentally
	// UPSERT other columns.
	SetLifecycleState(ctx context.Context, id string, state asset.LifecycleState) error
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
	// Envelope carries schema_version/event_id/idempotency_key/requested_at;
	// the v1 envelope contract is decoded and validated once, in
	// outboxevents.DecodeV1, not once per handler.
	outboxevents.Envelope
	AssetID string `json:"asset_id"`
}

// IndexDeleteHandler is the real handler for asset.index.delete_requested.v1.
//
// Both VectorPointDeleter and AssetDeleter are required for production wiring
// (BuildOutboxBundle populates the deleter field from
// qd.Runtime.Writer — see composition.go — and the assetDeleter from
// *assets.ClipsRepository). Tests pass in-memory stubs that
// satisfy the local interfaces.
type IndexDeleteHandler struct {
	log           *zap.Logger
	vectorDeleter VectorPointDeleter
	assetDeleter  AssetDeleter
}

// NewIndexDeleteHandler wires the producer-side dependencies. log nil →
// nop logger. Either deleter nil is a composition error and panics: a
// handler that silently skips the missing half would acknowledge the event
// with the deletion not performed, which is exactly the false-success class
// this handler exists to prevent (RA-07).
func NewIndexDeleteHandler(log *zap.Logger, vectorDeleter VectorPointDeleter, assetDeleter AssetDeleter) *IndexDeleteHandler {
	if log == nil {
		log = zap.NewNop()
	}
	if vectorDeleter == nil {
		panic("jobs.NewIndexDeleteHandler: VectorPointDeleter is required (godlike/05 wiring-error fail-fast)")
	}
	if assetDeleter == nil {
		panic("jobs.NewIndexDeleteHandler: AssetDeleter is required (godlike/05 wiring-error fail-fast)")
	}
	return &IndexDeleteHandler{
		log:           log.Named("index_delete"),
		vectorDeleter: vectorDeleter,
		assetDeleter:  assetDeleter,
	}
}

// EventType returns the canonical outboxevents constant.
func (h *IndexDeleteHandler) EventType() string {
	return outboxevents.EventAssetIndexDeleteRequested
}

// IdempotencyKey declares the canonical handler-level idempotency
// identity for asset.index.delete_requested events. Static — derived
// from the schema_version literal — so the HandlerRegistry.Register
// fail-closed panic can fire at init time if a future refactor
// forgets the declaration. See IndexingHandler.IdempotencyKey for the
// SSOT rationale (godlike/06 SSOT — one canonical owner per fact).
func (h *IndexDeleteHandler) IdempotencyKey() string {
	return outboxevents.EventAssetIndexDeleteRequested + "." + DeleteRequestSchemaVersion
}

// Handle parses the v1 envelope, performs the idempotent delete
// (pgvector + SQLite soft-delete), and returns nil on success. Validation
// failures and unsatisfiable payloads return typed terminal errors
// (PR1's outboxevents.NewTerminalError) so the pool's IsTerminal
// classifier dead-letters them immediately rather than burning
// max_attempts in a repair loop. Transient pgvector failures return
// non-terminal errors so the pool retries per its backoff.

func (h *IndexDeleteHandler) Handle(ctx context.Context, evt outboxevents.Event) error {
	log := h.log
	if log == nil {
		log = zap.NewNop()
	}
	if h.vectorDeleter == nil || h.assetDeleter == nil {
		return outboxevents.NewTerminalError(fmt.Errorf("%w: index delete dependencies are not wired", errIndexLifecycleTerminal))
	}

	req, reqLog, err := decodeIndexDeleteRequest(evt, log)
	if err != nil {
		return err
	}
	proceed, err := h.preflightIndexDelete(ctx, req, reqLog, log)
	if err != nil || !proceed {
		return err
	}
	if err := h.markIndexDeletePending(ctx, req, reqLog, log); err != nil {
		return err
	}
	if err := h.deleteIndexVectors(ctx, req, reqLog, log); err != nil {
		return err
	}
	if err := h.finalizeIndexDelete(ctx, req, reqLog, log); err != nil {
		return err
	}
	log.Info("asset.index.delete_requested: deletion complete", reqLog...)
	return nil
}

func decodeIndexDeleteRequest(evt outboxevents.Event, log *zap.Logger) (indexDeleteRequestV1, []zap.Field, error) {
	var req indexDeleteRequestV1
	if err := outboxevents.DecodeV1(outboxevents.DecodePolicy{
		EventName:         outboxevents.EventAssetIndexDeleteRequested,
		WantSchemaVersion: DeleteRequestSchemaVersion,
		Log:               log,
	}, evt, &req); err != nil {
		return indexDeleteRequestV1{}, nil, err
	}
	if req.AssetID == "" {
		log.Warn("asset.index.delete_requested: empty asset_id (terminal)",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
		)
		return indexDeleteRequestV1{}, nil, outboxevents.NewTerminalError(
			fmt.Errorf("asset.index.delete_requested: empty asset_id (terminal — retry cannot conjure an id)"),
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
	return req, reqLog, nil
}

// preflightIndexDelete returns false,nil for the idempotent-success cases and
// true,nil when the delete chain must proceed.
func (h *IndexDeleteHandler) preflightIndexDelete(ctx context.Context, req indexDeleteRequestV1, reqLog []zap.Field, log *zap.Logger) (bool, error) {
	existing, err := h.assetDeleter.GetClip(ctx, req.AssetID)
	if err != nil {
		log.Warn("asset.index.delete_requested: GetClip failed (retryable)",
			append(reqLog, zap.Error(err))...,
		)
		return false, fmt.Errorf("asset.index.delete_requested GetClip(%s): %w", req.AssetID, err)
	}
	if existing == nil {
		log.Info("asset.index.delete_requested: asset row absent — treat as success (idempotent)", reqLog...)
		return false, nil
	}
	switch string(existing.LifecycleState) {
	case string(asset.StateDeleted), "deleted", string(asset.StateIndexDeleted):
		log.Info("asset.index.delete_requested: asset already past INDEX_DELETED — treat as success (idempotent)",
			append(reqLog, zap.String("lifecycle_state", string(existing.LifecycleState)))...,
		)
		return false, nil
	}
	switch existing.LifecycleState {
	case asset.StateDriveDeleted:
		log.Info("asset.index.delete_requested: asset in DRIVE_DELETED — Drive side-effect confirmed; closing the index chain",
			append(reqLog, zap.String("lifecycle_state", string(existing.LifecycleState)))...,
		)
		return true, nil
	case asset.StateLifecycleIndexDeletePending:
		log.Info("asset.index.delete_requested: asset in INDEX_DELETE_PENDING — closing the chain",
			append(reqLog, zap.String("lifecycle_state", string(existing.LifecycleState)))...,
		)
		return true, nil
	default:
		log.Warn("asset.index.delete_requested: Drive-block guard fired — asset in unexpected lifecycle_state; file may still be alive in Drive",
			append(reqLog, zap.String("lifecycle_state", string(existing.LifecycleState)))...,
		)
		return false, terminalWrap(fmt.Errorf(
			"%w: drive_file_alive_block (asset row at %q instead of the expected post-Drive-confirmation state); the asset.index.delete_requested event arrived before DriveDeleteHandler confirmed the Drive side-effect (file ancora vivo / file still alive in user's Drive account); re-enqueue the index-delete event ONLY after DriveDeleteHandler has stamped DRIVE_DELETED — a retry against the current lifecycle_state will keep firing this guard",
			errIndexLifecycleTerminal, existing.LifecycleState,
		))
	}
}

func (h *IndexDeleteHandler) markIndexDeletePending(ctx context.Context, req indexDeleteRequestV1, reqLog []zap.Field, log *zap.Logger) error {
	if err := h.assetDeleter.SetIndexState(ctx, req.AssetID, asset.StateIndexDeletePending); err != nil {
		log.Warn("asset.index.delete_requested: SetIndexState(DELETE_PENDING) failed (retryable)",
			append(reqLog, zap.Error(err))...,
		)
		return fmt.Errorf("asset.index.delete_requested SetIndexState(DELETE_PENDING, %s): %w", req.AssetID, err)
	}
	return nil
}

func (h *IndexDeleteHandler) deleteIndexVectors(ctx context.Context, req indexDeleteRequestV1, reqLog []zap.Field, log *zap.Logger) error {
	log.Info("asset.index.delete_requested: deleting pgvector point", reqLog...)
	if err := h.vectorDeleter.DeleteAssetPoints(ctx, []string{req.AssetID}); err != nil {
		log.Warn("asset.index.delete_requested: pgvector delete failed (retryable)",
			append(reqLog, zap.Error(err))...,
		)
		return fmt.Errorf("asset.index.delete_requested DeletePoints(%s): %w", req.AssetID, err)
	}
	return nil
}

func (h *IndexDeleteHandler) finalizeIndexDelete(ctx context.Context, req indexDeleteRequestV1, reqLog []zap.Field, log *zap.Logger) error {
	if err := h.assetDeleter.SoftDelete(ctx, req.AssetID); err != nil {
		log.Warn("asset.index.delete_requested: SQLite SoftDelete failed (retryable — pgvector already done)",
			append(reqLog, zap.Error(err))...,
		)
		return fmt.Errorf("asset.index.delete_requested SoftDelete(%s): %w", req.AssetID, err)
	}
	if err := h.assetDeleter.SetIndexState(ctx, req.AssetID, asset.StateDELETED); err != nil {
		log.Warn("asset.index.delete_requested: SetIndexState(DELETED) failed (retryable)",
			append(reqLog, zap.Error(err))...,
		)
		return fmt.Errorf("asset.index.delete_requested SetIndexState(DELETED, %s): %w", req.AssetID, err)
	}
	if err := h.assetDeleter.SetLifecycleState(ctx, req.AssetID, asset.StateIndexDeleted); err != nil {
		log.Warn("asset.index.delete_requested: SetLifecycleState(INDEX_DELETED) failed (retryable — pgvector+SoftDelete+index_state already done)",
			append(reqLog, zap.Error(err))...,
		)
		return fmt.Errorf("asset.index.delete_requested SetLifecycleState(INDEX_DELETED, %s): %w", req.AssetID, err)
	}
	if err := h.assetDeleter.SetLifecycleState(ctx, req.AssetID, asset.StateDeleted); err != nil {
		log.Warn("asset.index.delete_requested: SetLifecycleState(DELETED) failed (retryable — pgvector+SoftDelete+index_state+INDEX_DELETED already done)",
			append(reqLog, zap.Error(err))...,
		)
		return fmt.Errorf("asset.index.delete_requested SetLifecycleState(DELETED, %s): %w", req.AssetID, err)
	}
	return nil
}
