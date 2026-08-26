// Package outbox is the shared port-adapter package for cmd/admin's
// outbox-repair operations. It hosts the RepairAdapter (formerly
// outboxRepairAdapter, originally in cmd/admin/reconcile_qdrant_adapters.go)
// so that BOTH `package main` admin commands (cmd/admin/backfill_*.go)
// AND the cmd/admin/reconcile subpackage can import the same canonical
// adapter without a `package main` cross-import dependency cycle.
//
// Created in PR-PKG-SIZE-CMD-ADMIN-1 (July 2026) as a follow-on to
// the reconcile subcommand move: the adapter was previously
// package-local to cmd/admin (reconcile_qdrant_adapters.go) but is
// also referenced by non-moved admin commands
// (backfill_asset_embeddings.go + backfill_missing.go). Extracting
// it here breaks the dependency on `package main` for the moved
// reconcile subpackage. See architecture/issues.yaml
// PR-PKG-SIZE-CMD-ADMIN-1 for the migration plan + follow-up
// tracking.
package outbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// RepairAdapter satisfies the canonical outbox-repair port by
// writing directly to outbox_events + lightly bumping media_assets,
// bypassing outbox.Dispatcher.
//
// Rationale (vs. going through outbox.Dispatcher):
//   - Dispatcher.EnqueueAndIndex demands a fully-populated *asset.Asset
//     and constructs an event_key derived from clipindexer package
//     vars + a content_hash supplied by the caller. Calling it from
//     admin / reconcile-repair paths would require synthesising an
//     Asset and choosing a content hash that varies per call — both
//     undesirable.
//   - Reconcile-repair does NOT need the metadata-write side-effect
//     of Dispatcher (UpdateClipTx). All reconcile-repair needs is to
//     ENQUEUE an asset.index.requested.v1 event for the worker to
//     re-run IndexClip with the canonical row's current payload.
//   - Wiring direct to outboxevents.Repository keeps the adapter
//     thin (one tx per enqueue, v1 envelope built inline from a
//     typed schema-version constant) and avoids the ClipsUpserter
//     dependency cycle (production assets.ClipsRepository is NOT
//     visible at this admin path).
//
// Idempotency:
//
//   - Delete (EnqueueDelete): event_key is deterministic
//     ("delete:<assetID>"). Re-running --apply on the same asset
//     is collapsed at the SQLite level by ON CONFLICT(event_key)
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
type RepairAdapter struct {
	db            *sql.DB
	outboxRepo    *outboxevents.Repository
	schemaVersion string
}

// NewRepairAdapter constructs a RepairAdapter bound to the supplied
// SQLite DB + outboxevents repository + canonical V1 schema version.
//
// schemaVersion is the canonical V1 envelope schema name (typically
// outboxevents.ReindexEnvelopeV1Schema = "media_assets_v3"). It
// threads into the event_key as the canonical
// (assetID, schema_version, content_hash) tuple per PR 11 + Card 7.1.
func NewRepairAdapter(db *sql.DB, outboxRepo *outboxevents.Repository, schemaVersion string) *RepairAdapter {
	return &RepairAdapter{
		db:            db,
		outboxRepo:    outboxRepo,
		schemaVersion: schemaVersion,
	}
}

// EnqueueReindex inserts an asset.index.requested.v1 outbox event
// for the supplied assetID. The event_key is deterministic per
// (assetID, target_schema_version, full_content_hash) tuple, built
// via outboxevents.BuildReindexEnvelopeV1 (the canonical envelope
// builder, when force=false) or outboxevents.BuildReindexEnvelopeV1Force
// (when force=true). Two consecutive reconciler --apply runs on the
// same asset (no content change, force=false) collapse to a single
// outbox_events row via ON CONFLICT (event_key) DO NOTHING. A
// force=true enqueue (admin reindex) survives a prior force=false
// enqueue for the same (assetID, schema, source) tuple because the
// :force suffix differentiates the event_key.
//
// The content_hash is PASSED BY THE CALLER (not fetched here) per
// PR 11 (June 2026) — the canonical reconciler flow
// (internal/capabilities/reconciliation/service.go) already
// calls assets.SourceVersionFor(...) once per asset and threads
// the value here. Callers MUST hand in a non-empty contentHash;
// the adapter is fail-closed on empty (deterministic event_key
// requires a fingerprint).
//
// force (Card 7.1, July 2026): the admin reindex path passes
// force=true. Production reconciler --apply also passes force=true
// today (the operator's --apply IS the admin opt-in). The adapter
// routes through outboxevents.BuildReindexEnvelopeV1Force when
// force=true so the worker bypasses the source_version supersede
// gate.
func (a *RepairAdapter) EnqueueReindex(ctx context.Context, assetID, contentHash string, force bool) error {
	if assetID == "" {
		return errors.New("outbox.RepairAdapter.EnqueueReindex: assetID must not be empty")
	}
	if contentHash == "" {
		return errors.New("outbox.RepairAdapter.EnqueueReindex: contentHash must not be empty — PR 11 contract (deterministic event_key requires a fingerprint; caller must pre-fetch via assets.SourceVersionFor before invoking)")
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // standard commit-or-rollback idiom

	// Light parity bump: refresh updated_at so monitors can see
	// the reconcile-repair touched the row. We do NOT mutate
	// source_version (the worker's supersede gate reads
	// source_version from metadata).
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

// EnqueueDelete inserts an asset.index.delete_requested.v1 outbox
// event for the supplied assetID. The event_key is deterministic
// ("delete:<assetID>") so re-running --apply on the same asset is
// collapsed at the SQLite level by ON CONFLICT(event_key) DO
// NOTHING. The event_id field is a per-call UUID for audit tracing
// (required by IndexDeleteHandler.Handle) and is NOT used in the
// event_key.
func (a *RepairAdapter) EnqueueDelete(ctx context.Context, assetID string) error {
	if assetID == "" {
		return errors.New("outbox.RepairAdapter.EnqueueDelete: assetID must not be empty")
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
