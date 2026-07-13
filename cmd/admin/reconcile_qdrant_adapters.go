// cmd/admin/reconcile_qdrant_adapters.go — port adapters extracted
// from reconcile_qdrant.go (PR-RECONCILE-SPLIT, July 2026).
//
// Four adapter types bridging cmd/admin glue to the reconciler service ports:
//   - qdrantListerAdapter    → reconciler.QdrantLister
//   - qdrantPayloadAdapter   → reconciler.PayloadCleaner
//   - outboxRepairAdapter    → reconciler.OutboxRepairEnqueuer
//   - reconcileReaderAdapter → reconciler.SQLiteReader
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Marcuss-ops/PipelineGen/internal/application/qdrant/reconciler"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/indexing"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
)

// ── Port adapters (cmd/admin glue) ────────────────────────────────────

// qdrantListerAdapter wraps transport.Client.ScrollPoints to satisfy
// reconciler.QdrantLister. The reconciler sees only PointSnapshot (no
// leak of qdrant.ScrollPoint into the application layer).
type qdrantListerAdapter struct {
	client *transport.Client
}

func (a *qdrantListerAdapter) ScrollPoints(ctx context.Context, collection string, offset string, limit int) (reconciler.Points, error) {
	res, err := a.client.ScrollPoints(ctx, collection, offset, limit, nil)
	if err != nil {
		return reconciler.Points{}, err
	}
	out := reconciler.Points{
		NextOffset: res.NextOffset,
		Items:      make([]reconciler.PointSnapshot, len(res.Points)),
	}
	for i, p := range res.Points {
		out.Items[i] = reconciler.PointSnapshot{ID: p.ID, Payload: p.Payload}
	}
	return out, nil
}

// qdrantPayloadAdapter wraps transport.Client.DeletePayloadKeys. The
// collection is captured at construction so the reconciler call sites
// stay simple.
type qdrantPayloadAdapter struct {
	client *transport.Client
}

func (a *qdrantPayloadAdapter) DeletePayloadKeys(ctx context.Context, collection string, keys []string, pointIDs []string) error {
	return a.client.DeletePayloadKeys(ctx, collection, keys, pointIDs)
}

// outboxRepairAdapter satisfies reconciler.OutboxRepairEnqueuer by
// writing directly to outbox_events + lightly bumping media_assets,
// bypassing outbox.Dispatcher.
//
// Rationale (vs. going through outbox.Dispatcher):
//   - Dispatcher.EnqueueAndIndex demands a fully-populated *asset.Asset
//     and constructs an event_key derived from clipindexer package vars
//   - a content_hash supplied by the caller. Calling it from
//     reconcile-repair would require synthesising an Asset and choosing
//     a content hash that varies per reconcile run — both undesirable.
//   - Reconcile-repair does NOT need the metadata-write side-effect of
//     Dispatcher (UpdateClipTx). All reconcile-repair needs is to
//     ENQUEUE an asset.index.requested.v1 event for the worker to
//     re-run IndexClip with the canonical row's current payload.
//   - Wiring direct to outboxevents.Repository keeps the adapter thin
//     (one tx per enqueue, v1 envelope built inline from a typed
//     schema-version constant) and avoids the ClipsUpserter dependency
//     cycle (production assets.ClipsRepository is NOT visible at this
//     admin path).
//
// Idempotency:
//
//   - Delete (EnqueueDelete): event_key is deterministic
//     ("delete:<assetID>"). Re-running --apply on the same asset is
//     collapsed at the SQLite level by ON CONFLICT(event_key)
//     DO NOTHING — only the first run enqueues, subsequent runs
//     are no-ops. The event_id field is a per-call UUID for audit
//     tracing (required by IndexDeleteHandler) and is NOT used in
//     the event_key.
//
//   - Reindex (EnqueueReindex): event_key is deterministic per
//     (assetID, target_schema_version, full_content_hash) tuple,
//     built via outboxevents.BuildReindexEnvelopeV1 — the canonical
//     envelope builder. PR 11 (June 2026) replaces the prior
//     uuid-suffixed key with this deterministic shape so two
//     consecutive `reconcile-qdrant --apply` runs on the same
//     asset (no content change) collapse to a single outbox_events
//     row via ON CONFLICT (event_key) DO NOTHING. A hash change
//     produces a fresh row and the worker downstream re-evaluates
//     against the new source_version (supersede gate still owns
//     the "is the event actually still current?" question at
//     execution time).
type outboxRepairAdapter struct {
	db            *sql.DB
	outboxRepo    *outboxevents.Repository
	schemaVersion string
}

// EnqueueReindex inserts an asset.index.requested.v1 outbox event for
// the supplied assetID. The event_key is deterministic per
// (assetID, target_schema_version, full_content_hash) tuple, built via
// outboxevents.BuildReindexEnvelopeV1 (the canonical envelope
// builder, when force=false) or outboxevents.BuildReindexEnvelopeV1Force
// (when force=true) — see those functions' idempotency invariants in
// PR 11 (June 2026) + Card 7.1 (July 2026). Two consecutive reconciler
// --apply runs on the same asset (no content change, force=false) collapse
// to a single outbox_events row via ON CONFLICT (event_key) DO NOTHING.
// A force=true enqueue (admin reindex) survives a prior force=false
// enqueue for the same (assetID, schema, source) tuple because the
// :force suffix differentiates the event_key.
//
// The content_hash lookup happens INSIDE the producer tx so the
// captured value is exactly the row-state at the moment we commit
// (Snapshot isolation: the row is read through the same tx that
// stamps updated_at + inserts the outbox row). Empty content_hash
// is fail-closed — without a fingerprint we cannot derive a
// deterministic event_key, so we abort rather than emit a
// silently-collapsing event that the worker could never route
// (the worker compares payload.source_version against
// metadata_json.$.content_hash; an empty source_version matches
// every empty row, which would silently no-op at execution time).
//
// The hash priority mirrors the consumer-side readSourceVersion
// priority list in application/jobs/outbox/indexing.go so the
// producer and the worker agree on what counts as "the current
// fingerprint" without a JOIN round-trip:
//
//  1. metadata_json.$.content_hash  ← dispatcher atomic write
//  2. metadata_json.$.file_hash     ← non-dispatcher ingest fallback
//  3. media_assets.file_hash        ← legacy top-level column
//
// This list is duplicated here (vs imported) so the cmd/admin
// package does not pick up the indexing.go dependency for what is
// really a 3-line COALESCE pattern; if the priority changes,
// the change propagates here + there.
//
// PR 11 (June 2026, follow-up): the content_hash is now PASSED BY
// THE CALLER, not fetched here. The canonical reconciler flow
// (internal/application/qdrant/reconciler/service.go) already
// calls assets.SourceVersionFor(...) once per asset and threads
// the value here, so duplicating the COALESCE priority chain
// (metadata_json.$.content_hash → metadata_json.$.file_hash →
// media_assets.file_hash) inside this adapter is misuse-prone.
// Callers MUST hand in a non-empty contentHash; the adapter is
// fail-closed on empty (deterministic event_key requires a
// fingerprint). See
// internal/infrastructure/database/sqlite/assets/source_version.go
// + source_version_test.go for the regression pin across all
// four priority slots (including the legacy top-level column).
//
// force (Card 7.1, July 2026): the admin reindex path passes
// force=true. Production reconciler --apply also passes force=true
// today (the operator's --apply IS the admin opt-in). The adapter
// routes through outboxevents.BuildReindexEnvelopeV1Force when
// force=true so the worker bypasses the source_version supersede
// gate.
func (a *outboxRepairAdapter) EnqueueReindex(ctx context.Context, assetID, contentHash string, force bool) error {
	if assetID == "" {
		return errors.New("outboxRepairAdapter.EnqueueReindex: assetID must not be empty")
	}
	if contentHash == "" {
		return errors.New("outboxRepairAdapter.EnqueueReindex: contentHash must not be empty — PR 11 contract (deterministic event_key requires a fingerprint; caller must pre-fetch via assets.SourceVersionFor before invoking)")
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // standard commit-or-rollback idiom

	// Light parity bump: refresh updated_at so monitors can see the
	// reconcile-repair touched the row. We do NOT mutate source_version
	// (the worker's supersede gate reads source_version from metadata).
	nowStr := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx,
		`UPDATE media_assets SET updated_at = ? WHERE id = ?`,
		nowStr, assetID,
	); err != nil {
		return fmt.Errorf("update updated_at %s: %w", assetID, err)
	}

	var eventKey, payloadJSON string
	if force {
		eventKey, payloadJSON, err = outboxevents.BuildReindexEnvelopeV1Force(assetID, a.schemaVersion, contentHash, time.Now())
	} else {
		eventKey, payloadJSON, err = outboxevents.BuildReindexEnvelopeV1(assetID, a.schemaVersion, contentHash, time.Now())
	}
	if err != nil {
		return fmt.Errorf("build reindex envelope: %w", err)
	}
	if _, err := a.outboxRepo.Enqueue(
		ctx, tx,
		outboxevents.EventAssetIndexRequested,
		assetID, "media_asset",
		payloadJSON, eventKey,
	); err != nil {
		return fmt.Errorf("enqueue outbox reindex event: %w", err)
	}
	return tx.Commit()
}

// EnqueueDelete inserts an asset.index.delete_requested.v1 outbox event
// for the supplied assetID. The event_key is deterministic
// ("delete:<assetID>") so re-running --apply on the same asset is
// collapsed at the SQLite level by ON CONFLICT(event_key) DO NOTHING.
// The event_id field is a per-call UUID for audit tracing (required by
// IndexDeleteHandler.Handle) and is NOT used in the event_key.
func (a *outboxRepairAdapter) EnqueueDelete(ctx context.Context, assetID string) error {
	if assetID == "" {
		return errors.New("outboxRepairAdapter.EnqueueDelete: assetID must not be empty")
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // standard commit-or-rollback idiom

	// Stamp DELETE_PENDING so dashboards show the in-flight delete
	// even if the worker crashes mid-process.
	nowStr := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx,
		`UPDATE media_assets SET lifecycle_state = 'DELETE_PENDING', deleted_at = ?, updated_at = ? WHERE id = ?`,
		nowStr, nowStr, assetID,
	); err != nil {
		return fmt.Errorf("set DELETE_PENDING %s: %w", assetID, err)
	}

	eventID := uuid.NewString()
	eventKey := "delete:" + assetID
	payload := map[string]any{
		"schema_version":  "asset.index.delete_requested.v1",
		"event_id":        eventID,
		"asset_id":        assetID,
		"requested_at":    nowStr,
		"idempotency_key": eventKey,
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal v1 delete payload: %w", err)
	}
	if _, err := a.outboxRepo.Enqueue(
		ctx, tx,
		outboxevents.EventAssetIndexDeleteRequested,
		assetID, "media_asset",
		string(payloadJSON), eventKey,
	); err != nil {
		return fmt.Errorf("enqueue outbox delete event: %w", err)
	}
	return tx.Commit()
}

// reconcileReaderAdapter wraps indexing.SQLiteAssetStore.ListAssetsForReconcile.
type reconcileReaderAdapter struct {
	store *indexing.SQLiteAssetStore
}

func (a *reconcileReaderAdapter) ListForReconcile(ctx context.Context, includeLifecycleStates []string) ([]reconciler.AssetSnapshot, error) {
	rows, err := a.store.ListAssetsForReconcile(ctx, includeLifecycleStates)
	if err != nil {
		return nil, err
	}
	out := make([]reconciler.AssetSnapshot, len(rows))
	for i, r := range rows {
		out[i] = reconciler.AssetSnapshot{
			ID:             r.ID,
			WorkspaceID:    r.WorkspaceID,
			LifecycleState: r.LifecycleState,
			ContentHash:    r.ContentHash,
		}
	}
	return out, nil
}
