// Package event owns the canonical identity of every CROSS-SLICE outbox
// event name and envelope schema version.
//
// godlike/06 "one owner per fact": an event name is an identity fact. It is
// provider-scoped across BOTH storage engines (SQLite and PostgreSQL) and
// must stay byte-identical, so it cannot be declared once per engine
// adapter. Before this package existed the same literal was declared twice:
//
//	internal/platform/sqlite/outboxevents/registry.go  → EventAssetIndexRequested
//	internal/platform/postgres/media/outbox.go         → EventAssetIndexRequested
//
// with a comment promising the two copies would "remain byte-identical".
// Two copies of a fact are not an SSOT — they are a drift hazard no scanner
// caught, because every scanner exempted both copies. The literal now lives
// HERE and the engine adapters re-export it (a reference, not a second
// declaration).
//
// Ownership rules:
//
//   - This package declares the literal. It is a stdlib-only leaf package
//     (percheck_kernel_boundary), so every zone may import it.
//   - Engine adapters (sqlite/outboxevents, postgres/media) keep their
//     historical exported names as compile-time re-exports so existing
//     callers are untouched.
//   - No other production file may re-declare a literal listed in
//     Canonical(); the forward-prevention gate percheck_identity_ssot fails
//     closed on any re-declaration.
//
// Adding a new canonical event: append the constant AND the Canonical()
// registry entry in the same change. The gate reads Canonical(), so a
// literal that is not registered is simply not yet protected — it is never
// silently half-protected.
package event

// Canonical outbox event names (outbox_events.event_type).
//
// The asset.index.* family keeps its prefix symmetric with the sibling
// index events so one substring search finds every producer, consumer and
// test. This is the naming convention the SQLite adapter documented before
// the identity moved here.
const (
	// AssetIndexRequested asks the outbox consumer to (re)index one asset.
	AssetIndexRequested = "asset.index.requested"
	// AssetIndexDeleteRequested asks the consumer to drop the asset's index.
	AssetIndexDeleteRequested = "asset.index.delete_requested"
	// AssetIndexRestoreRequested re-enters the indexing pipeline after a
	// restore (emitted by the restore hop of the deletion state machine).
	AssetIndexRestoreRequested = "asset.index.restore_requested"
	// AssetDriveDeleteRequested is the first hop of the deletion state
	// machine (ACTIVE → DELETE_REQUESTED → DRIVE_DELETE_PENDING → …).
	AssetDriveDeleteRequested = "asset.drive.delete_requested"

	// BindingIndexRequested reindexes a media_concepts row after a
	// media_bindings mutation.
	BindingIndexRequested = "binding.index.requested"

	// ScriptGenerateQueued announces an accepted script.generate submission.
	ScriptGenerateQueued = "script.generate.queued"

	// VoiceoverCleanupRequested durably deletes superseded voiceover Drive
	// artifacts instead of a detached goroutine.
	VoiceoverCleanupRequested = "voiceover.cleanup.requested"

	// AssetPublished announces a publish on a destination.
	AssetPublished = "asset.published"

	// AssetRightsChanged announces a rights-surface mutation that must be
	// re-projected into the semantic index.
	AssetRightsChanged = "asset.rights.changed"

	// AssetRightsExtensionBatchApplied is the migration-158 propagation
	// channel for existing rows (one event per apply, not per row).
	AssetRightsExtensionBatchApplied = "asset.rights_extension.batch_applied"

	// DeliveryRequested requests a delivery of an artifact.
	DeliveryRequested = "delivery.requested"

	// AssetMetadataExportRequested requests a metadata export.
	AssetMetadataExportRequested = "asset.metadata_export.requested"

	// ProviderSyncRequested requests a provider synchronization.
	ProviderSyncRequested = "provider.sync.requested"

	// WorkflowStepCompleted / WorkflowStepFailed are the workflow-step
	// terminal markers.
	WorkflowStepCompleted = "workflow.step.completed"
	WorkflowStepFailed    = "workflow.step.failed"

	// JobCompleted is emitted transactionally with the terminal job status
	// flip (SUCCEEDED or FAILED).
	JobCompleted = "job.completed"

	// MetadataEnrichRequested is the durable async metadata-enrichment
	// request (Sept 2026): the commit emits it in the SAME transaction as
	// media_assets so LLM analysis runs outside the extraction critical path.
	MetadataEnrichRequested = "metadata.enrich.requested"
)

// Canonical envelope schema_version values.
//
// A schema version is a separate identity fact from the event name: the
// consumer fails fast when the inbound envelope's schema_version does not
// match its expected literal, so a producer that stamps a different string
// breaks the consumer with no retry able to cure it.
const (
	AssetIndexRequestedV1Schema        = "asset.index.requested.v1"
	AssetIndexDeleteRequestedV1Schema  = "asset.index.delete_requested.v1"
	AssetIndexRestoreRequestedV1Schema = "asset.index.restore_requested.v1"
	AssetDriveDeleteRequestedV1Schema  = "asset.drive.delete_requested.v1"
	VoiceoverCleanupRequestedV1Schema  = "voiceover.cleanup.requested.v1"
	AssetPublishedV1Schema             = "asset.published.v1"
	AssetRightsChangedV1Schema         = "asset.rights.changed.v1"
	AssetRightsExtensionBatchV1Schema  = "asset.rights_extension.batch_applied.v1"
	BindingIndexRequestedV1Schema      = "binding.index.requested.v1"
)

// ArtifactStagedV1 is the versioned event name used by the staging →
// drive-delivery hop (the event name itself carries the v1 suffix, so it is
// an event name, not a separate schema constant).
const ArtifactStagedV1 = "artifact.staged.v1"
