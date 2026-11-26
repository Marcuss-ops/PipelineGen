package outboxevents

import (
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/event"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/outbox"
)

// event-type constants are shared between producers (Dispatcher.Enqueue,
// EnqueueDeleteAndDelete) and consumers (Handler.EventType). The ticket
// docs use "media.index.*" in prose; the codebase standard adopted here
// is "asset.index.*" because that prefix matches the sibling Indexing
// event (asset.index.requested) and keeps greppability symmetric across
// the outbox-events column. The VECTOR-002 ticket's "media.index.*"
// naming maps to these constants 1:1.
//
// IDENTITY NOTE: the literal values are owned by internal/kernel/event
// (godlike/06 one owner per fact). This block is a compile-time re-export so
// the SQLite adapter and the PostgreSQL adapter can never drift: both resolve
// the same constant. A re-declaration of any literal here is a
// percheck_identity_ssot violation.
const (
	EventAssetIndexRequested       = event.AssetIndexRequested
	EventAssetIndexDeleteRequested = event.AssetIndexDeleteRequested
	// EventAssetIndexRestoreRequested is the canonical event-type
	// emitted by mutations.AssetMutationDispatcher.EnqueueAndRestore.
	// Handler (deferred to task 3 of 5, currently
	// mutations.AssetMutationDispatcher is foundation-only) consumes
	// this event and completes the picture with pgvector re-upsert +
	// lifecycle_state flip back to 'ready'.
	//
	// Naming follows the established asset.index.* family so a single
	// substring search finds the producer + consumer + tests on the
	// same grep pass.
	EventAssetIndexRestoreRequested = event.AssetIndexRestoreRequested

	// EventAssetDriveDeleteRequested (Blocco 3.1, June 2026) — first
	// hop of the deletion state machine
	// (ACTIVE → DELETE_REQUESTED → DRIVE_DELETE_PENDING → INDEX_DELETE_PENDING → DELETED).
	//
	// Producer: outbox.Dispatcher.EnqueueDriveDelete (replaces the
	// pre-Blocco 3.1 atomic EnqueueAndDelete which was a best-effort
	// single-shot). Consumer: internal/capabilities/jobs.DriveDeleteHandler
	// calls drive.FileLifecycle.Trash (or .Delete for hard-delete),
	// then transitions lifecycle_state → INDEX_DELETE_PENDING and
	// emits EventAssetIndexDeleteRequested in the same tx so a
	// worker crash mid-flow is recoverable.
	//
	// Naming follows the established asset.* family — substring
	// search for `asset.drive.delete_requested` finds producer +
	// consumer + tests in one grep pass.
	EventAssetDriveDeleteRequested = event.AssetDriveDeleteRequested

	EventDeliveryRequested            = event.DeliveryRequested
	EventAssetMetadataExportRequested = event.AssetMetadataExportRequested
	EventProviderSyncRequested        = event.ProviderSyncRequested
	EventWorkflowStepCompleted        = event.WorkflowStepCompleted
	EventWorkflowStepFailed           = event.WorkflowStepFailed
	EventScriptGenerateQueued         = event.ScriptGenerateQueued

	// EventJobCompleted is emitted transactionally with the terminal job
	// status flip (SUCCEEDED or FAILED) so derived projections that need
	// the finalized run report (performance_runs / performance_steps) can
	// be rebuilt without a manual backfill. aggregate_id is the job id.
	EventJobCompleted = event.JobCompleted

	// EventVoiceoverCleanupRequested (P0.7 Wave 21, Step 10/12, June 2026).
	// Replaces the pre-fix fire-and-forget `cleanupOrphanVoiceover`
	// goroutine (detached via context.Background) with a durable
	// outbox event that survives handler cancel + server restart.
	// Producer (voiceover.finalizeStage) enqueues this event INSIDE
	// the same SQL tx as the voiceovers UPSERT + media_assets
	// projection UPSERT, so all four writes commit atomically; a
	// rollback discards all four. The consumer
	// (voiceover.outbox.VoiceoverCleanupHandler) deletes OLD Drive
	// files ONLY when `old_drive_file_id != new_drive_file_id`,
	// removes old local files, and returns retryable errors on
	// transient Drive failures so the pool's exponential backoff
	// retries per its config.
	EventVoiceoverCleanupRequested = event.VoiceoverCleanupRequested

	// EventAssetPublished (SEMANTIC-LOCATION-API-2026-07-06 Wave 5,
	// July 2026). Emitted by the CALLER of Publisher.Publish() —
	// outbox.Dispatcher gets a typed EnqueueAssetPublished(port,
	// payload) in Wave 6. Carries the rich payload — destination,
	// origin, category, subject, provider, drive_file_id, drive_path,
	// tags — so the consumer (internal/capabilities/jobs.AssetPublishedHandler)
	// composes a richer pgvector embed text WITHOUT re-loading these
	// semantic fields from media_assets.
	//
	// Coexist with EventAssetIndexRequested (pre-existing
	// vector-incoming ingestion): each event is independent and a
	// caller can emit one OR both. Per godlike/07 minimum-blast-radius
	// the new event is purely ADDITIVE — no existing producer or
	// consumer is rewritten.
	EventAssetPublished = event.AssetPublished

	// EventAssetRightsChanged (PR-CLIPINGEST-PIPELINE Step 10,
	// July 2026). Emitted by producers that mutate the rights
	// surface on a media_assets row (e.g. operator upgrades
	// rights_status from review_required → licensed; or sets
	// expires_at when a license ends). The consumer (handler
	// lands in follow-up PR — see suggest_followups) re-emits
	// EventAssetIndexRequested so the pgvector reindex path picks
	// up the new rights-state payload without a separate
	// indexer-writer code path. Naming follows the
	// established asset.* family so a single substring search
	// finds producer + consumer + tests in one grep pass.
	//
	// Coexist with EventAssetIndexRequested: the rights event
	// is independently emitted by a rights-changing workflow; a
	// caller can emit one OR both (typically just the rights
	// event for a permission-only change; both for a content-
	// AND-rights bundle change).
	//
	// godlike/07 fail-closed (matches EventAssetPublished): the
	// schema-version mismatch path is a terminal sentinel per
	// godlike/06 — the handler fails-fast with a typed error so
	// the producer MUST upgrade before consumers can resume.
	EventAssetRightsChanged = event.AssetRightsChanged

	// EventAssetRightsExtensionBatchApplied is the migration-158
	// propagation channel for existing rows. The migration emits
	// ONE such event per apply (NOT one per row — bad blast
	// radius on a larger fleet). The consumer (follow-up PR)
	// schedules targeted re-projection for the affected assets
	// via a startup-time reconcile sweep. Field-name consistency
	// with EventAssetRightsChanged is intentional so a future
	// per-row upgrade reuses the same payload codec.
	EventAssetRightsExtensionBatchApplied = event.AssetRightsExtensionBatchApplied

	// EventBindingIndexRequested is emitted by the canonical
	// BindingMutationDispatcher whenever a media_bindings row is
	// mutated (Create/Update/Approve/Reject/Delete). The consumer
	// reindexes the parent media_concepts row in pgvector so the
	// semantic projection stays consistent with the authoritative
	// SQLite state.
	EventBindingIndexRequested = event.BindingIndexRequested
)

// JobCompletedEventKey returns the canonical outbox_events.event_key for
// the job.completed event. It is the SINGLE dedup vector shared by every
// producer of the event — SQLiteStore.Complete/Fail, the
// JobFinalizer.CompleteWithArtifacts path, and the Sender-side completion
// services (Service.emitOutboxEvents / WithArtifactsService.emitArtifactOutboxEvents).
// A job reaches a terminal state at most once, so the key is the job id
// alone (no attempt segment): a retry or a cross-path re-completion of the
// same job collapses to one outbox row via ON CONFLICT(event_key) DO NOTHING
// instead of enqueuing a duplicate job.completed event.
func JobCompletedEventKey(jobID string) string {
	return outbox.JobCompletedEventKey(jobID)
}

// SchemaVersionAssetPublished is the canonical v1 schema string.
// The consumer (AssetPublishedHandler) fails-fast with a typed
// Terminal sentinel if the inbound envelope's schema_version
// does not match this string literally; mismatch cannot be cured
// by retry, so producers must upgrade.
const SchemaVersionAssetPublished = event.AssetPublishedV1Schema

// SchemaVersionAssetRightsChanged is the canonical v1 schema string
// for the per-row rights-change event. The consumer (handler
// lands in follow-up PR per godlike/07 minimum-blast-radius;
// see suggest_followups for the precise migration path) fails-
// fast with a typed Terminal sentinel if the inbound envelope's
// schema_version does not match this string literally; mismatch
// cannot be cured by retry, so producers MUST upgrade.
const SchemaVersionAssetRightsChanged = event.AssetRightsChangedV1Schema

// SchemaVersionAssetRightsExtensionBatchApplied is the canonical
// v1 schema string for the migration-apply batch event.
const SchemaVersionAssetRightsExtensionBatchApplied = event.AssetRightsExtensionBatchV1Schema

// Handler and HandlerRegistry are the ENGINE-INDEPENDENT consumer contract,
// owned by internal/kernel/outbox. These aliases keep the SQLite package's
// public surface stable for the adapter and its tests while the capability
// layer depends on the kernel contract instead.
type Handler = outbox.Handler

type HandlerRegistry = outbox.HandlerRegistry

func NewHandlerRegistry() *HandlerRegistry {
	return outbox.NewHandlerRegistry()
}
