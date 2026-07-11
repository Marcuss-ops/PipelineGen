// Package metadataexport — canonical outboxevents.Handler for
// asset.metadata_export.requested.v1 events.
//
// Step 2 of the post-architettura 2026 plan (June 2026): the
// MetadataExportHandler USED to live in
// internal/application/jobs/outbox/metadata_export.go alongside the
// SQL queries and FS writers. After split, the handler is the thin
// entry point: parse envelope → Validate → resolve assets → dispatch
// to Service (filesystem write). All cross-layer concerns reach the
// canonical typed ports (AssetResolver, ExportWriter) so this file is
// infrastructure-free.
//
// Implementation notes:
//
//   - The handler composes the new Service rather than inlining its
//     logic. Composition root wires both.
package metadataexport

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// MetadataExportRequestSchemaVersion is the canonical, EXACT string
// the MetadataExportHandler accepts. Producers MUST send
// "asset.metadata_export.requested.v1" literally. Mismatch is
// TERMINAL — no retry — so producers upgrade instead of silently
// retrying on what looks like a routine failure. Mirrors the
// DriveDeleteRequestSchemaVersion / VoiceoverCleanupSchemaVersion
// pattern, and is the canonical entry for IdempotencyKey() so the
// suffix shape stays consistent across handlers.
const MetadataExportRequestSchemaVersion = "asset.metadata_export.requested.v1"

// MetadataExportHandler is the canonical outboxevents.Handler for
// asset.metadata_export.requested.v1 events. Composition root
// constructs ONE instance and registers it via HandlerRegistry.
// Handler is safe for concurrent invocation — it shares only
// the (logger, resolver, writer, service) fields, all documented-safe.
type MetadataExportHandler struct {
	log     *zap.Logger
	service *Service
}

// HandlerDeps bundles the wiring-time dependencies for the canonical
// ctor. nil log is tolerated (zap.NewNop on construction). Resolver
// and Writer MAY be nil in tests; the handler treats nil ports as
// terminal failures at Handle time (Service.ExportFilesystem returns
// the typed-port-missing errors that map to ErrResolverUnavailable /
// ErrWriterUnavailable).
type HandlerDeps struct {
	Resolver  AssetResolver
	Writer    ExportWriter
	OutputDir string
	Log       *zap.Logger
}

// NewMetadataExportHandler constructs the canonical outboxevents.Handler
// from typed-port deps. The Service is built internally so callers
// (composition root, tests) bind a single struct rather than
// reach-through to two constructors.
//
// Signature change vs the pre-Step-2 constructor `(log, *sql.DB, dir)`:
// the new ctor takes a HandlerDeps struct. Tests and the composition
// root are updated in lockstep — see
// internal/application/jobs/outbox/workflow_step_completed_test.go
// + internal/app/build_bundles_process.go::BuildOutboxBundle.
func NewMetadataExportHandler(deps HandlerDeps) *MetadataExportHandler {
	return &MetadataExportHandler{
		log:     deps.Log,
		service: NewService(deps.Log, deps.Resolver, deps.Writer, deps.OutputDir),
	}
}

// EventType returns the canonical outboxevents constant. Mirrors the
// IndexingHandler.EventType pattern in
// internal/application/jobs/outbox/indexing.go.
func (h *MetadataExportHandler) EventType() string {
	return outboxevents.EventAssetMetadataExportRequested
}

// IdempotencyKey implements outboxevents.Handler (Fase 6(c) Push 6.2,
// July 2026). Static canonical form: `<event_type>.v1` so the
// HandlerRegistry.Register fail-closed panic fires at init time if
// a future refactor forgets the declaration. Per-event idempotency
// keys flow through the envelope's IdempotencyKey field and do NOT
// substitute this static handler-class declaration.
//
// Note on interpretation (godlike/06 SSOT): IdempotencyKey() is a
// STATIC handler-class identifier — it does NOT change per-event.
// Per-event dedup keys live in the outbox_events.event_key column
// (driven by the payload's IdempotencyKey field) and are surfaced
// separately. The static shape here is the canonical SSOT entry
// used by the metrics namespace and the Register-time fail-closed
// panic.
func (h *MetadataExportHandler) IdempotencyKey() string {
	return outboxevents.EventAssetMetadataExportRequested + "." + MetadataExportRequestSchemaVersion
}

// Handle is the canonical event-processing entry point. Parses the v1
// envelope, validates it strictly, resolves asset_ids (either from the
// envelope's explicit list or via the AssetResolver's job-scope
// query), and dispatches to Service.ExportFilesystem for the actual
// filesystem work. Drive destinations log+ack (no upload); filesystem
// destinations write atomically.
//
// Behaviour summary (mirrors the pre-Step-2 contract bit-for-bit):
//
//   - 2xx-equivalent (write succeeded)        → nil error → MarkCompleted.
//   - 4xx-equivalent (terminal envelope fail) → err wrapping
//     ErrMetadataTerminal → outboxevents handler classifies as
//     terminal → MarkCompleted (no retry).
//   - 5xx/network/db error during write/query → non-nil error →
//     outbox pool retries.
func (h *MetadataExportHandler) Handle(ctx context.Context, evt outboxevents.Event) error {
	log := h.log
	if log == nil {
		log = zap.NewNop()
	}

	var req MetadataExportRequest
	if err := json.Unmarshal([]byte(evt.PayloadJSON), &req); err != nil {
		log.Warn("asset.metadata_export.requested payload parse failed (terminal)",
			zap.Int64("event_id", evt.ID),
			zap.Error(err),
		)
		return fmt.Errorf("%w: parse: %s", ErrMetadataTerminal, err.Error())
	}
	if err := Validate(&req); err != nil {
		log.Warn("asset.metadata_export.requested envelope validation failed",
			zap.Int64("event_id", evt.ID),
			zap.Error(err),
		)
		return err
	}

	ids, err := h.resolveAssetIDs(ctx, &req)
	if err != nil {
		log.Warn("asset.metadata_export.requested asset_id resolution failed",
			zap.Int64("event_id", evt.ID),
			zap.Error(err),
		)
		return fmt.Errorf("asset.metadata_export.requested resolve asset_ids: %w", err)
	}

	if len(ids) == 0 {
		log.Info("asset.metadata_export.requested: no assets resolved — completed with empty result",
			zap.String("job_id", req.JobID),
			zap.Int64("event_id", evt.ID),
		)
		return nil
	}

	switch req.Destination.Provider {
	case DestinationDrive:
		// Drive uploads are owned by the canonical upload pipeline
		// (internal/upload/drive), which owns token plumbing, resumable
		// uploads, quota lifecycle. The outbox sidecar export is the
		// LOCAL copy the consumer already has — Drive is a durability
		// mirror driven from the same payload. We ack to keep the
		// audit row consistent without doubling the upload logic.
		log.Info("asset.metadata_export.requested acknowledged — drive upload handled by upload pipeline",
			zap.String("folder_id", req.Destination.FolderID),
			zap.Int("asset_count", len(ids)),
			zap.String("idempotency_key", req.IdempotencyKey),
			zap.Int64("event_id", evt.ID),
		)
		return nil
	case DestinationFilesystem:
		return h.service.ExportFilesystem(ctx, evt.ID, &req, ids)
	default:
		// Defensive — Validate already screens this in production.
		// Marked terminal so the outbox pool dead-letters even if a
		// future validate-drift passes an unknown provider.
		return fmt.Errorf("%w: destination.provider=%q unknown", ErrMetadataTerminal, req.Destination.Provider)
	}
}

// resolveAssetIDs fills the asset_ids slice from job_id when the
// producer supplied only a scope. Direct envelope order:
//   - explicit asset_ids when supplied (audit-friendly: producer
//     knows the scope).
//   - AssetResolver.ResolveAssetIDs(job_id) when only job_id is given.
//   - empty when neither is supplied (Validate already screens this).
func (h *MetadataExportHandler) resolveAssetIDs(ctx context.Context, r *MetadataExportRequest) ([]string, error) {
	if len(r.AssetIDs) > 0 {
		return r.AssetIDs, nil
	}
	if r.JobID == "" || h.service == nil || h.service.resolver == nil {
		return nil, nil
	}
	return h.service.resolver.ResolveAssetIDs(ctx, r.JobID, nil)
}
