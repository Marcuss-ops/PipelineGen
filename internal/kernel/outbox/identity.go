// identity.go — compile-time re-exports of the outbox event vocabulary.
//
// The literals are OWNED by internal/kernel/event (one owner per fact). Every
// declaration here is an alias, never a second string literal, so this package,
// the SQLite adapter and the PostgreSQL adapter cannot drift
// (percheck_identity_ssot).
package outbox

import "github.com/Marcuss-ops/PipelineGen/internal/kernel/event"

const (
	EventAssetIndexRequested        = event.AssetIndexRequested
	EventAssetIndexDeleteRequested  = event.AssetIndexDeleteRequested
	EventAssetIndexRestoreRequested = event.AssetIndexRestoreRequested

	EventAssetDriveDeleteRequested        = event.AssetDriveDeleteRequested
	EventDeliveryRequested                = event.DeliveryRequested
	EventAssetMetadataExportRequested     = event.AssetMetadataExportRequested
	EventProviderSyncRequested            = event.ProviderSyncRequested
	EventWorkflowStepCompleted            = event.WorkflowStepCompleted
	EventWorkflowStepFailed               = event.WorkflowStepFailed
	EventScriptGenerateQueued             = event.ScriptGenerateQueued
	EventJobCompleted                     = event.JobCompleted
	EventVoiceoverCleanupRequested        = event.VoiceoverCleanupRequested
	EventAssetPublished                   = event.AssetPublished
	EventAssetRightsChanged               = event.AssetRightsChanged
	EventAssetRightsExtensionBatchApplied = event.AssetRightsExtensionBatchApplied
	EventBindingIndexRequested            = event.BindingIndexRequested
)

// SchemaVersionAssetPublished is the canonical v1 schema string.
// The consumer (AssetPublishedHandler) fails-fast with a typed Terminal
// sentinel if the inbound envelope's schema_version does not match this string
// literally; mismatch cannot be cured by retry, so producers must upgrade.
const SchemaVersionAssetPublished = event.AssetPublishedV1Schema

// SchemaVersionAssetRightsChanged is the canonical v1 schema string for the
// per-row rights-change event.
const SchemaVersionAssetRightsChanged = event.AssetRightsChangedV1Schema

// SchemaVersionAssetRightsExtensionBatchApplied is the canonical v1 schema
// string for the migration-apply batch event.
const SchemaVersionAssetRightsExtensionBatchApplied = event.AssetRightsExtensionBatchV1Schema

// JobCompletedEventKey returns the canonical outbox_events.event_key for the
// job.completed event. It is the SINGLE dedup vector shared by every producer
// of the event — SQLiteStore.Complete/Fail, the JobFinalizer path, and the
// Sender-side completion services.
//
// A job reaches a terminal state at most once, so the key is the job id alone
// (no attempt segment): a retry or a cross-path re-completion of the same job
// collapses to one outbox row via ON CONFLICT(event_key) DO NOTHING instead of
// enqueuing a duplicate job.completed event.
func JobCompletedEventKey(jobID string) string {
	return EventJobCompleted + ":" + jobID
}
