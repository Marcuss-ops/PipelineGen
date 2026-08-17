package outboxevents

import (
	"context"
	"fmt"
	"sync"
)

// event-type constants are shared between producers (Dispatcher.Enqueue,
// EnqueueDeleteAndDelete) and consumers (Handler.EventType). The ticket
// docs use "media.index.*" in prose; the codebase standard adopted here
// is "asset.index.*" because that prefix matches the sibling Indexing
// event (asset.index.requested) and keeps greppability symmetric across
// the outbox-events column. The QDRANT-002 ticket's "media.index.*"
// naming maps to these constants 1:1.
const (
	EventAssetIndexRequested       = "asset.index.requested"
	EventAssetIndexDeleteRequested = "asset.index.delete_requested"
	// EventAssetIndexRestoreRequested is the canonical event-type
	// emitted by mutations.AssetMutationDispatcher.EnqueueAndRestore.
	// Handler (deferred to task 3 of 5, currently
	// mutations.AssetMutationDispatcher is foundation-only) consumes
	// this event and completes the picture with Qdrant re-upsert +
	// lifecycle_state flip back to 'ready'.
	//
	// Naming follows the established asset.index.* family so a single
	// substring search finds the producer + consumer + tests on the
	// same grep pass.
	EventAssetIndexRestoreRequested = "asset.index.restore_requested"

	// EventAssetDriveDeleteRequested (Blocco 3.1, June 2026) — first
	// hop of the deletion state machine
	// (ACTIVE → DELETE_REQUESTED → DRIVE_DELETE_PENDING → INDEX_DELETE_PENDING → DELETED).
	//
	// Producer: outbox.Dispatcher.EnqueueDriveDelete (replaces the
	// pre-Blocco 3.1 atomic EnqueueAndDelete which was a best-effort
	// single-shot). Consumer: application/jobs/outbox.DriveDeleteHandler
	// calls drive.FileLifecycle.Trash (or .Delete for hard-delete),
	// then transitions lifecycle_state → INDEX_DELETE_PENDING and
	// emits EventAssetIndexDeleteRequested in the same tx so a
	// worker crash mid-flow is recoverable.
	//
	// Naming follows the established asset.* family — substring
	// search for `asset.drive.delete_requested` finds producer +
	// consumer + tests in one grep pass.
	EventAssetDriveDeleteRequested = "asset.drive.delete_requested"

	EventDeliveryRequested            = "delivery.requested"
	EventAssetMetadataExportRequested = "asset.metadata_export.requested"
	EventProviderSyncRequested        = "provider.sync.requested"
	EventWorkflowStepCompleted        = "workflow.step.completed"
	EventWorkflowStepFailed           = "workflow.step.failed"
	EventScriptGenerateQueued         = "script.generate.queued"

	// EventJobCompleted is emitted transactionally with the terminal job
	// status flip (SUCCEEDED or FAILED) so derived projections that need
	// the finalized run report (performance_runs / performance_steps) can
	// be rebuilt without a manual backfill. aggregate_id is the job id.
	EventJobCompleted = "job.completed"

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
	EventVoiceoverCleanupRequested = "voiceover.cleanup.requested"

	// EventAssetPublished (SEMANTIC-LOCATION-API-2026-07-06 Wave 5,
	// July 2026). Emitted by the CALLER of Publisher.Publish() —
	// outbox.Dispatcher gets a typed EnqueueAssetPublished(port,
	// payload) in Wave 6. Carries the rich payload — destination,
	// origin, category, subject, provider, drive_file_id, drive_path,
	// tags — so the consumer (application/jobs/outbox.AssetPublishedHandler)
	// composes a richer Qdrant embed text WITHOUT re-loading these
	// semantic fields from media_assets.
	//
	// Coexist with EventAssetIndexRequested (pre-existing
	// Qdrant-incoming ingestion): each event is independent and a
	// caller can emit one OR both. Per godlike/07 minimum-blast-radius
	// the new event is purely ADDITIVE — no existing producer or
	// consumer is rewritten.
	EventAssetPublished = "asset.published"

	// EventAssetRightsChanged (PR-CLIPINGEST-PIPELINE Step 10,
	// July 2026). Emitted by producers that mutate the rights
	// surface on a media_assets row (e.g. operator upgrades
	// rights_status from review_required → licensed; or sets
	// expires_at when a license ends). The consumer (handler
	// lands in follow-up PR — see suggest_followups) re-emits
	// EventAssetIndexRequested so the Qdrant reindex path picks
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
	EventAssetRightsChanged = "asset.rights.changed"

	// EventAssetRightsExtensionBatchApplied is the migration-158
	// propagation channel for existing rows. The migration emits
	// ONE such event per apply (NOT one per row — bad blast
	// radius on a larger fleet). The consumer (follow-up PR)
	// schedules targeted re-projection for the affected assets
	// via a startup-time reconcile sweep. Field-name consistency
	// with EventAssetRightsChanged is intentional so a future
	// per-row upgrade reuses the same payload codec.
	EventAssetRightsExtensionBatchApplied = "asset.rights_extension.batch_applied"

	// EventBindingIndexRequested is emitted by the canonical
	// BindingMutationDispatcher whenever a media_bindings row is
	// mutated (Create/Update/Approve/Reject/Delete). The consumer
	// reindexes the parent media_concepts row in Qdrant so the
	// semantic projection stays consistent with the authoritative
	// SQLite state.
	EventBindingIndexRequested = "binding.index.requested"
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
	return EventJobCompleted + ":" + jobID
}

// SchemaVersionAssetPublished is the canonical v1 schema string.
// The consumer (AssetPublishedHandler) fails-fast with a typed
// Terminal sentinel if the inbound envelope's schema_version
// does not match this string literally; mismatch cannot be cured
// by retry, so producers must upgrade.
const SchemaVersionAssetPublished = "asset.published.v1"

// SchemaVersionAssetRightsChanged is the canonical v1 schema string
// for the per-row rights-change event. The consumer (handler
// lands in follow-up PR per godlike/07 minimum-blast-radius;
// see suggest_followups for the precise migration path) fails-
// fast with a typed Terminal sentinel if the inbound envelope's
// schema_version does not match this string literally; mismatch
// cannot be cured by retry, so producers MUST upgrade.
const SchemaVersionAssetRightsChanged = "asset.rights.changed.v1"

// SchemaVersionAssetRightsExtensionBatchApplied is the canonical
// v1 schema string for the migration-apply batch event.
const SchemaVersionAssetRightsExtensionBatchApplied = "asset.rights_extension.batch_applied.v1"

type Handler interface {
	EventType() string
	// IdempotencyKey declares the canonical handler-level identifier
	// used for SQL ON CONFLICT(event_key) dedup + observability
	// grouping. godlike/07 fail-closed: every Handler MUST return a
	// non-empty value; an empty return fails-fast at Register via
	// panic so a missing handler identity is impossible to ship to
	// production.
	//
	// The convention is a stable string shaped `<event_type>.<scheme>`
	// (e.g. "asset.index.requested.v1"). The key is NOT the per-event
	// idempotency key in the envelope's payload — those are
	// producer-supplied and unique-per-event; this is the
	// handler-side canonical declaration that ties a handler class
	// to a dedup / metrics namespace.
	IdempotencyKey() string
	Handle(ctx context.Context, evt Event) error
}

type HandlerRegistry struct {
	mu       sync.RWMutex
	handlers map[string]Handler
}

func NewHandlerRegistry() *HandlerRegistry {
	return &HandlerRegistry{handlers: make(map[string]Handler)}
}

func (r *HandlerRegistry) Register(h Handler) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if h == nil {
		return fmt.Errorf("handler is nil")
	}
	key := h.EventType()
	if key == "" {
		return fmt.Errorf("handler event type is empty")
	}
	// Fase 6(c) Push 6.2 (July 2026): every handler MUST declare an
	// idempotency_key at registration time. Empty panic-at-init
	// (godlike/07 fail-closed at observable boundary) so a handler
	// without an idempotency identity is impossible to ship.
	idemKey := h.IdempotencyKey()
	if idemKey == "" {
		panic(fmt.Sprintf("outboxevents.HandlerRegistry.Register: handler for %q returned empty IdempotencyKey() (godlike/07 fail-closed at init)", key))
	}
	if _, exists := r.handlers[key]; exists {
		return fmt.Errorf("handler already registered for %s", key)
	}
	r.handlers[key] = h
	return nil
}

func (r *HandlerRegistry) Get(eventType string) (Handler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.handlers[eventType]
	return h, ok
}
