// Package finalization defines the canonical domain contracts for the
// transactional job-finalization spine (Spina Dorsale, Fase 1, July 2026).
//
// Every pipeline capability (YouTube, stock, Artlist, images, voiceover,
// sound effects, uploads) converges on the same spine:
//
//	Capability
//	    ↓
//	Produce VerifiedArtifact (validate + hash)
//	    ↓
//	Publish via ArtifactPreparationService (publish idempotente)
//	    ↓
//	JobFinalizer.CompleteWithArtifacts (transazione atomica)
//	    ↓  BEGIN
//	    ↓    write asset canonico
//	    ↓    write asset location
//	    ↓    write source version
//	    ↓    write result manifest
//	    ↓    write job artifacts
//	    ↓    write outbox events
//	    ↓    job → SUCCEEDED
//	    ↓  COMMIT
//	    ↓
//	Outbox consumer → Qdrant / proiezioni esterne
//
// A job never becomes SUCCEEDED before all required artifacts are
// finalised. The completion, asset records, locations, and outbox events
// are written in the SAME SQLite transaction.
//
// Canonical reference: Piano d'Azione Completo § 4.
package finalization

import (
	"encoding/json"
	"time"
)

// ── ArtifactKind ────────────────────────────────────────────────────

// ArtifactKind classifies the high-level category of a produced artifact.
type ArtifactKind string

const (
	KindVideo       ArtifactKind = "video"
	KindImage       ArtifactKind = "image"
	KindAudio       ArtifactKind = "audio"
	KindDocument    ArtifactKind = "document"
	KindScript      ArtifactKind = "script"
	KindVoiceover   ArtifactKind = "voiceover"
	KindSoundEffect ArtifactKind = "sound_effect"
	KindMetadata    ArtifactKind = "metadata"
	KindArchive     ArtifactKind = "archive"
)

// ── PublishAction ───────────────────────────────────────────────────

// PublishAction describes what the publisher actually did on the remote
// storage backend (Drive, object storage, etc.).
//
// The zero value is the empty string so that a zero-valued
// PublishedArtifact is distinguishable from a real publish outcome.
// Consumers that branch on Action MUST default the empty branch to a
// conservative no-op.
//
// Consolidation note (July 2026): delivery.PublishAction in
// internal/application/assets/delivery/types.go mirrors these four
// constants. FASE 5 (Drive Publisher-only) will make that package
// alias this one — no new duplication after the cutover.
type PublishAction string

const (
	PublishCreated PublishAction = "created"
	PublishUpdated PublishAction = "updated"
	PublishSkipped PublishAction = "skipped"
	PublishRenamed PublishAction = "renamed"
)

// Valid returns true if a is one of the four canonical actions.
func (a PublishAction) Valid() bool {
	switch a {
	case PublishCreated, PublishUpdated, PublishSkipped, PublishRenamed:
		return true
	}
	return false
}

// ── AssetLocation ───────────────────────────────────────────────────

// AssetLocation is the canonical descriptor for where a published
// artifact physically lives. Every PublishedArtifact carries exactly
// one AssetLocation.
type AssetLocation struct {
	// Provider identifies the storage backend (e.g. "drive", "s3").
	Provider string `json:"provider"`

	// FileID is the provider-specific file identifier.
	// For Drive: the Google Drive file ID.
	FileID string `json:"file_id"`

	// WebViewLink is the human-readable URL to view the file.
	WebViewLink string `json:"web_view_link,omitempty"`

	// DownloadLink is the direct download URL for the file.
	// Consumers MUST read this from the canonical location — never
	// reconstruct via string interpolation.
	DownloadLink string `json:"download_link,omitempty"`

	// Checksum is the provider-returned checksum (typically MD5 for
	// Drive). Distinct from the artifact's content SHA-256.
	Checksum string `json:"checksum,omitempty"`

	// FolderID is the resolved folder identifier on the provider.
	FolderID string `json:"folder_id,omitempty"`

	// FolderPath is the human-readable folder path.
	FolderPath string `json:"folder_path,omitempty"`

	// Action is what the publisher actually did.
	Action PublishAction `json:"action,omitempty"`
}

// ── VerifiedArtifact ────────────────────────────────────────────────

// VerifiedArtifact represents an artifact that has been locally
// validated (content hash computed, size verified) but has NOT yet
// been published to a remote location.
//
// This is the output of a capability's production step. The
// ArtifactPreparationService transforms it into a PublishedArtifact.
type VerifiedArtifact struct {
	// ArtifactID is the unique canonical identifier for this artifact.
	// Derived from a content hash (SHA-256) or a deterministic
	// capability-level ID.
	ArtifactID string `json:"artifact_id"`

	// Kind is the high-level category of the artifact.
	Kind ArtifactKind `json:"kind"`

	// Filename is the desired filename (without path) for publication.
	Filename string `json:"filename"`

	// LocalPath is the absolute path to the artifact on local disk.
	LocalPath string `json:"local_path"`

	// MIMEType is the IANA media type (e.g. "video/mp4", "image/png").
	MIMEType string `json:"mime_type"`

	// SizeBytes is the artifact size in bytes.
	SizeBytes int64 `json:"size_bytes"`

	// SHA256 is the hex-encoded SHA-256 digest of the artifact content.
	SHA256 string `json:"sha256"`

	// SourceVersion is the logical version of the source that produced
	// this artifact. Monotonically increasing within a capability.
	SourceVersion int64 `json:"source_version"`

	// Required indicates whether this artifact MUST be successfully
	// finalised for the job to become SUCCEEDED. Optional artifacts
	// that fail do not block job completion.
	Required bool `json:"required"`

	// IdempotencyKey is a deterministic key that makes publication
	// and finalisation idempotent. Same content → same key.
	IdempotencyKey string `json:"idempotency_key"`
}

// ── PublishedArtifact ───────────────────────────────────────────────

// PublishedArtifact represents an artifact that has been successfully
// published to a remote location. It extends VerifiedArtifact with
// the canonical AssetLocation.
//
// This is the input to AssetFinalizerTx.FinalizeAsset.
type PublishedArtifact struct {
	// ArtifactID is the unique canonical identifier for this artifact.
	ArtifactID string `json:"artifact_id"`

	// Kind is the high-level category of the artifact.
	Kind ArtifactKind `json:"kind"`

	// Filename is the filename as published on the remote location.
	Filename string `json:"filename"`

	// MIMEType is the IANA media type.
	MIMEType string `json:"mime_type"`

	// SizeBytes is the artifact size in bytes.
	SizeBytes int64 `json:"size_bytes"`

	// SHA256 is the hex-encoded SHA-256 digest of the artifact content.
	SHA256 string `json:"sha256"`

	// SourceVersion is the logical version of the source.
	SourceVersion int64 `json:"source_version"`

	// Required indicates whether this artifact is required for job
	// completion.
	Required bool `json:"required"`

	// IdempotencyKey is the same deterministic key from the
	// VerifiedArtifact, carried forward for idempotent finalisation.
	IdempotencyKey string `json:"idempotency_key"`

	// Location is the canonical descriptor of where the artifact was
	// published.
	Location AssetLocation `json:"location"`
}

// ── ResultManifest ──────────────────────────────────────────────────

// ResultManifest is the envelope for a job's result. It carries the
// schema version, job identity, attempt counter, and capability-
// specific result data.
type ResultManifest struct {
	// SchemaVersion identifies the manifest schema (e.g. "v1").
	SchemaVersion string `json:"schema_version"`

	// JobID is the canonical job identifier.
	JobID string `json:"job_id"`

	// WorkflowID is the optional parent workflow identifier.
	WorkflowID string `json:"workflow_id,omitempty"`

	// Attempt is the job attempt counter. Used for stale-attempt
	// detection.
	Attempt int `json:"attempt"`

	// Data is the capability-specific result payload. Opaque to the
	// finalizer; stored verbatim.
	Data json.RawMessage `json:"data"`
}

// ── OutboxEvent ─────────────────────────────────────────────────────

// OutboxEvent is a domain-level descriptor for an event that must be
// committed atomically alongside the job completion. The concrete
// payload and event type are capability-specific.
type OutboxEvent struct {
	// EventType is the canonical event type string (e.g.
	// "asset.index_requested.v1").
	EventType string `json:"event_type"`

	// AggregateID identifies the domain aggregate this event belongs to
	// (typically the asset ID or job ID).
	AggregateID string `json:"aggregate_id"`

	// EventKey is a deterministic deduplication key.
	EventKey string `json:"event_key"`

	// Payload is the event-specific JSON payload.
	Payload json.RawMessage `json:"payload"`
}

// ── ArtifactRef ─────────────────────────────────────────────────────

// ArtifactRef is a lightweight reference to a finalised artifact,
// returned by AssetFinalizerTx.FinalizeAsset. It carries the minimum
// information needed for downstream consumers (indexing, workflow
// coordination) to reference the artifact without carrying its full
// payload.
type ArtifactRef struct {
	// ArtifactID is the canonical artifact identifier.
	ArtifactID string `json:"artifact_id"`

	// AssetID is the canonical asset identifier in the asset catalog.
	AssetID string `json:"asset_id"`

	// Kind is the artifact category.
	Kind ArtifactKind `json:"kind"`

	// SourceVersion is the logical source version at finalisation time.
	SourceVersion int64 `json:"source_version"`

	// ContentHash is the SHA-256 digest.
	ContentHash string `json:"content_hash"`

	// Location is the canonical published location.
	Location AssetLocation `json:"location"`
}

// ── FinalizationRequest ─────────────────────────────────────────────

// FinalizationRequest is the input to JobFinalizer.CompleteWithArtifacts.
// It carries the lease (for ownership verification), the result
// manifest, all published artifacts, and any outbox events to emit
// atomically.
type FinalizationRequest struct {
	// Lease is the job lease held by the calling worker. The finalizer
	// validates that the lease is still valid and belongs to the
	// calling worker before committing.
	Lease Lease `json:"lease"`

	// Result is the job's result manifest.
	Result ResultManifest `json:"result"`

	// Artifacts is the list of published artifacts to register.
	Artifacts []PublishedArtifact `json:"artifacts"`

	// Events is the list of outbox events to emit atomically with the
	// job completion.
	Events []OutboxEvent `json:"events,omitempty"`
}

// ── FinalizationResult ──────────────────────────────────────────────

// FinalizationResult is returned by JobFinalizer.CompleteWithArtifacts
// on success. It carries the artifact references for downstream
// consumers (workflow coordinator, dashboards).
type FinalizationResult struct {
	// JobID is the canonical job identifier.
	JobID string `json:"job_id"`

	// Status is the terminal job status (SUCCEEDED).
	Status string `json:"status"`

	// CompletedAt is the UTC timestamp of transaction commit.
	CompletedAt time.Time `json:"completed_at"`

	// ArtifactRefs is the list of finalised artifact references.
	ArtifactRefs []ArtifactRef `json:"artifact_refs"`
}

// ── Lease ───────────────────────────────────────────────────────────

// Lease represents a claimed job lease. The finalizer validates that the
// lease is still valid (not expired) and belongs to the calling worker
// before committing the completion transaction.
//
// Mapping note (July 2026): jobs.Lease in internal/application/jobs/
// carries a *job.Job pointer; this domain Lease carries a flat JobID
// string instead, avoiding coupling the domain layer to the infrastructure
// Job struct. FASE 2's JobFinalizer adapter will map between the two.
type Lease struct {
	// LeaseID is the unique lease identifier assigned at claim time.
	LeaseID string `json:"lease_id"`

	// JobID is the canonical job identifier this lease is for.
	JobID string `json:"job_id"`

	// WorkerID identifies the worker that holds this lease.
	WorkerID string `json:"worker_id"`

	// Attempt is the job attempt counter at the time the lease was
	// claimed. Must match the job's current attempt for the commit
	// to succeed.
	Attempt int `json:"attempt"`

	// ExpiresAt is the UTC timestamp after which the lease is
	// considered expired.
	ExpiresAt time.Time `json:"expires_at"`
}

// Valid reports whether the lease has not yet expired.
func (l Lease) Valid() bool {
	return time.Now().UTC().Before(l.ExpiresAt)
}
