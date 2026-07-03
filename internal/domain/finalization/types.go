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
// Required vs Optional artifacts (P1.2, July 2026):
//
//   - Required artifacts block job completion. If any required artifact
//     is missing from the publish-side at completion time, the
//     JobFinalizer returns ErrRequiredArtifactMissing.
//   - Optional artifacts are non-blocking. JobFinalizer records every
//     optional artifact in FinalizationResult.OptionalArtifactReport
//     (a typed-data audit sidecar) regardless of outcome. The optional
//     artifact's status — Finalized / Missing / Failed — is preserved
//     for operator investigation. Optional failures DO NOT fail the job.
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

// ── ArtifactRequirement (P1.2) ──────────────────────────────────────

// ArtifactRequirement classifies whether an artifact blocks job
// completion or is non-blocking (best-effort, recorded but does not
// fail the job). Replaces the legacy `Required bool` field on
// VerifiedArtifact and PublishedArtifact (P1.2 cutover).
//
// godlike/07 typed-error contract: the zero value
// (ArtifactRequirementInvalid) is explicitly rejected at validation
// time so a caller that forgot to set the field fail-closes loudly
// (mirrors PublishAction's empty-string zero-value handling).
//
// Canonical values:
//
//   - ArtifactRequirementRequired — blocks job completion. Missing
//     at completion → ErrRequiredArtifactMissing.
//   - ArtifactRequirementOptional — non-blocking. JobFinalizer records
//     the artifact in OptionalArtifactRecord (per-optional audit
//     sidecar) regardless of outcome.
type ArtifactRequirement int

const (
	// ArtifactRequirementInvalid is the zero value. Exists so the
	// field is explicitly NOT-Required and NOT-Optional until the
	// caller sets it; rejected by validateRequest so a default-zero
	// artifact cannot silently pass the gate as "Optional".
	ArtifactRequirementInvalid ArtifactRequirement = iota

	// ArtifactRequirementRequired marks an artifact whose absence
	// at completion time fails the request with ErrRequiredArtifactMissing.
	ArtifactRequirementRequired

	// ArtifactRequirementOptional marks a non-blocking artifact.
	// JS completion succeeds even when the optional artifact is
	// missing or failed; the per-optional outcome is recorded in
	// FinalizationResult.OptionalArtifactReport for audit.
	ArtifactRequirementOptional
)

// Valid returns true if r is one of the two canonical values
// (Required or Optional). ArtifactRequirementInvalid (zero value)
// is NOT valid; callers MUST set Requirement explicitly.
func (r ArtifactRequirement) Valid() bool {
	switch r {
	case ArtifactRequirementRequired, ArtifactRequirementOptional:
		return true
	}
	return false
}

// String returns the human-readable label ("REQUIRED" or "OPTIONAL")
// used in structured logs and the job_events audit row. The zero
// value renders as "INVALID" so a wrong-default sentinel surfaces
// loudly at log scrape time.
func (r ArtifactRequirement) String() string {
	switch r {
	case ArtifactRequirementRequired:
		return "REQUIRED"
	case ArtifactRequirementOptional:
		return "OPTIONAL"
	}
	return "INVALID"
}

// ── OptionalArtifactStatus (P1.2) ───────────────────────────────────

// OptionalArtifactStatus classifies the outcome of an optional
// artifact's per-request attempt. The JobFinalizer records one
// OptionalArtifactRecord per optional artifact in
// FinalizationResult.OptionalArtifactReport and persists a durable
// copy in the `optional_artifact_report` job_events row.
type OptionalArtifactStatus int

const (
	// OptionalArtifactStatusUnknown is the zero value. The
	// JobFinalizer MUST never produce a record in this state.
	OptionalArtifactStatusUnknown OptionalArtifactStatus = iota

	// OptionalArtifactStatusFinalized means the artifact was
	// successfully published and is present in the request's
	// Artifacts list (matched by ArtifactID).
	OptionalArtifactStatusFinalized

	// OptionalArtifactStatusMissing means the worker declared the
	// artifact (in OptionalDeclarations) but did NOT publish it.
	// No underlying error — the worker intentionally skipped the
	// artifact (e.g. condition that made the artifact irrelevant).
	OptionalArtifactStatusMissing

	// OptionalArtifactStatusFailed means the worker attempted to
	// publish but ArtifactPreparation returned an error. The underlying
	// error is preserved in OptionalArtifactRecord.Err so an operator
	// can investigate WITHOUT needing to correlate against a separate
	// error log.
	OptionalArtifactStatusFailed
)

// Valid returns true if s is one of the three canonical values
// (Finalized, Missing, Failed). The zero value (Unknown) is NOT valid.
func (s OptionalArtifactStatus) Valid() bool {
	switch s {
	case OptionalArtifactStatusFinalized,
		OptionalArtifactStatusMissing,
		OptionalArtifactStatusFailed:
		return true
	}
	return false
}

// String returns the human-readable label for logs / job_events.
func (s OptionalArtifactStatus) String() string {
	switch s {
	case OptionalArtifactStatusFinalized:
		return "FINALIZED"
	case OptionalArtifactStatusMissing:
		return "MISSING"
	case OptionalArtifactStatusFailed:
		return "FAILED"
	}
	return "UNKNOWN"
}

// ── ArtifactDeclaration (P1.2) ──────────────────────────────────────

// ArtifactDeclaration is the worker's registry of an artifact it
// INTENDED to handle during the job, marked with its Requirement.
// The JobFinalizer cross-references OptionalDeclarations against
// the request's actually-published `Artifacts` list to build
// FinalizationResult.OptionalArtifactReport.
//
// For required artifacts, the worker publishes the artifact directly
// into `request.Artifacts` — a declaration is OPTIONAL (the cross-ref
// path is a fallback). For optional artifacts, an explicit declaration
// lets the worker pre-signal the outcome (Finalized / Missing / Failed)
// without depending on cross-referencing inference.
//
// Either way, every optional artifact's outcome is recorded in the
// audit sidecar so operators have an at-a-glance count of
// success/missing/failed without correlating against separate error
// logs.
type ArtifactDeclaration struct {
	// ArtifactID is the canonical artifact identifier the worker
	// intends to handle. Matches by ArtifactID against the request's
	// Artifacts list.
	ArtifactID string `json:"artifact_id"`

	// Kind is the high-level category of the artifact.
	Kind ArtifactKind `json:"kind"`

	// Filename is the optional canonical publication filename.
	Filename string `json:"filename,omitempty"`

	// MIMEType is the optional IANA media type.
	MIMEType string `json:"mime_type,omitempty"`

	// IdempotencyKey is the deterministic key the ArtifactPreparation
	// service would use for publication. Carried for audit; the
	// JobFinalizer does not enforce uniqueness across declarations.
	IdempotencyKey string `json:"idempotency_key,omitempty"`

	// Requirement classifies the declaration. MUST equal
	// ArtifactRequirementOptional — declaring a required artifact
	// in OptionalDeclarations is a programming error
	// (ErrOptionalDeclarationHasRequiredRequirement).
	Requirement ArtifactRequirement `json:"requirement"`

	// Status is the worker's pre-publish intent for this artifact.
	//
	//   - OptionalArtifactStatusFinalized: the worker produced the
	//     artifact and includes it in `request.Artifacts`. The
	//     JobFinalizer MUST verify the ArtifactID appears in
	//     `request.Artifacts` — when missing, returns
	//     ErrOptionalArtifactFinalizedMismatch.
	//
	//   - OptionalArtifactStatusMissing: the worker did NOT produce
	//     the artifact (silent — no error). Recorded as Missing.
	//
	//   - OptionalArtifactStatusFailed: the worker attempted to
	//     produce but ArtifactPreparation returned an error. Recorded
	//     as Failed with Err carrying the underlying typed sentinel
	//     (preserves errors.Is/As traversal).
	Status OptionalArtifactStatus `json:"status"`

	// Err is the underlying failure carrier when Status ==
	// OptionalArtifactStatusFailed. Nil otherwise.
	Err error `json:"-"`
}

// ── OptionalArtifactRecord (P1.2) ───────────────────────────────────

// OptionalArtifactRecord is the per-optional-artifact audit sidecar
// entry on FinalizationResult. JobFinalizer.CompleteWithArtifacts
// populates one record per optional artifact (regardless of outcome)
// and persists a JSON-encoded copy of the entire report inside the
// same SQLite transaction under the `optional_artifact_report` job_events
// row (separate from the `job_completed` event).
//
// Why a sidecar?
//
//   job_completed alone tells operators "this job succeeded" but
//   says nothing about why some optional artifacts are missing or
//   failed (success today can paper over hidden degradation: an
//   AI image that never generated, a metadata.json that never
//   uploaded, etc.). The sidecar carries the EXACT per-optional
//   outcome so dashboards can surface the degradation to operators
//   without parsing text logs.
type OptionalArtifactRecord struct {
	// ArtifactID is the canonical identifier of the optional artifact.
	ArtifactID string `json:"artifact_id"`

	// Kind is the artifact category (preserved from the declaration /
	// artifact for dashboards that aggregate by Kind).
	Kind ArtifactKind `json:"kind"`

	// Requirement is ALWAYS ArtifactRequirementOptional for the
	// records on this struct; included for JSON schema symmetry with
	// ArtifactDeclaration so the audit row reads without case-split.
	Requirement ArtifactRequirement `json:"requirement"`

	// Status is the per-artifact outcome (Finalized / Missing / Failed).
	Status OptionalArtifactStatus `json:"status"`

	// Err is the underlying failure carrier when Status == Failed.
	// In-memory-runtime only (json:"-") — used by callers during the
	// same process for errors.Is / errors.As traversal. The
	// JSON-persistent carrier is ErrorMessage below.
	Err error `json:"-"`

	// ErrorMessage is the JSON-persistent string carrier for the
	// underlying failure when Status == Failed. Populated from
	// Err.Error() at JobFinalizer.CompleteWithArtifacts time so the
	// `optional_artifact_report` job_events row can carry the
	// failure reason verbatim through standard JSON marshaling.
	// (Err has json:"-" so it is otherwise stripped during the
	// job_events data_json marshal.)
	ErrorMessage string `json:"error_message,omitempty"`

	// Filename is the canonical publication filename when known —
	// overwritten with the resolved value from the matched
	// PublishedArtifact when Status == Finalized, falling back to
	// the declaration's intended filename otherwise.
	Filename string `json:"filename,omitempty"`

	// IdempotencyKey is the deterministic key when known —
	// overwritten with the resolved value from the matched
	// PublishedArtifact when Status == Finalized, falling back to
	// the declaration's intended key otherwise.
	IdempotencyKey string `json:"idempotency_key,omitempty"`

	// RecordedAt is the UTC timestamp the record was assembled by
	// the JobFinalizer. Useful for sequencing optional outcomes in
	// dashboards when the worker took Time-on-step.
	RecordedAt time.Time `json:"recorded_at"`
}

// ── VerifiedArtifact ────────────────────────────────────────────────

// VerifiedArtifact represents an artifact that has been locally
// validated (content hash computed, size verified) but has NOT yet
// been published to a remote location.
//
// This is the output of a capability's production step. The
// ArtifactPreparationService transforms it into a PublishedArtifact.
//
// P1.2 (July 2026): the `Required bool` field is replaced by the typed
// `Requirement ArtifactRequirement` enum. The zero value
// (ArtifactRequirementInvalid) is explicitly rejected by the
// JobFinalizer at validation time so callers cannot accidentally
// default to "Optional" by leaving the field unset.
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

	// Requirement classifies whether this artifact blocks job
	// completion. Set explicitly to ArtifactRequirementRequired for
	// block-on-missing artifacts, ArtifactRequirementOptional for
	// best-effort sidecars. The zero value (ArtifactRequirementInvalid)
	// is rejected at validation — callers MUST set this field.
	Requirement ArtifactRequirement `json:"requirement"`

	// IdempotencyKey is a deterministic key that makes publication
	// and finalisation idempotent. Same content → same key.
	IdempotencyKey string `json:"idempotency_key"`
}

// ── PublishedArtifact ────────────────────────────────────────────────

// PublishedArtifact represents an artifact that has been successfully
// published to a remote location. It extends VerifiedArtifact with
// the canonical AssetLocation.
//
// This is the input to AssetFinalizerTx.FinalizeAsset.
//
// P1.2 (July 2026): the `Required bool` field is replaced by the typed
// `Requirement ArtifactRequirement` enum carried through from
// VerifiedArtifact. The ArtifactPreparation service preserves the
// requirement during the local→remote publish step. Clean cutover —
// no back-compat alias per godlike/06 one-owner-per-fact.
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

	// Requirement classifies whether this artifact blocks job
	// completion (P1.2). Carried verbatim from VerifiedArtifact.Requirement
	// through ArtifactPreparation.Prepare. JobFinalizer uses this
	// typed field for the cross-reference against OptionalDeclarations.
	Requirement ArtifactRequirement `json:"requirement"`

	// IdempotencyKey is the deterministic key the worker supplied.
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
// manifest, all published artifacts, optional artifact declarations,
// and any outbox events to emit atomically.
//
// P1.2 (July 2026): the request carries two artefact-side surfaces:
//
//   - `Artifacts` — the actually-published artifacts (locations
//     returned by ArtifactPreparation). Required artifacts MUST
//     appear here, or CompleteWithArtifacts returns
//     ErrRequiredArtifactMissing.
//   - `OptionalDeclarations` — the worker's per-optional-artifact
//     intent (Finalized/Missing/Failed). Optional: the cross-ref
//     can infer optional outcomes by filtering `Artifacts` against
//     `Requirement == ArtifactRequirementOptional`, but explicit
//     declarations let the worker pre-signal Missing/Failed without
//     running an unsuccessful publish.
type FinalizationRequest struct {
	// Lease is the job lease held by the calling worker. The finalizer
	// validates that the lease is still valid and belongs to the
	// calling worker before committing.
	Lease Lease `json:"lease"`

	// Result is the job's result manifest.
	Result ResultManifest `json:"result"`

	// Artifacts is the list of published artifacts to register.
	Artifacts []PublishedArtifact `json:"artifacts"`

	// OptionalDeclarations (P1.2) is the worker's per-optional-artifact
	// intent. Each entry classifies a declared optional artifact as
	// Finalized, Missing, or Failed. The JobFinalizer cross-references
	// against Artifacts and persists the resolved audit report both
	// on FinalizationResult.OptionalArtifactReport (in-memory) and
	// inside the `optional_artifact_report` job_events row (durable).
	//
	// May be empty — the JobFinalizer's fallback path infers optional
	// outcomes from Artifacts (filter Requirement == Optional →
	// Finalized record). Explicit declarations take precedence when
	// present.
	OptionalDeclarations []ArtifactDeclaration `json:"optional_declarations,omitempty"`

	// Events is the list of outbox events to emit atomically with the
	// job completion.
	Events []OutboxEvent `json:"events,omitempty"`
}

// ── FinalizationResult ──────────────────────────────────────────────

// FinalizationResult is returned by JobFinalizer.CompleteWithArtifacts
// on success. It carries the artifact references for downstream
// consumers (workflow coordinator, dashboards) AND, in P1.2, the
// optional-artifact audit sidecar.
type FinalizationResult struct {
	// JobID is the canonical job identifier.
	JobID string `json:"job_id"`

	// Status is the terminal job status (SUCCEEDED).
	Status string `json:"status"`

	// CompletedAt is the UTC timestamp of transaction commit.
	CompletedAt time.Time `json:"completed_at"`

	// ArtifactRefs is the list of finalised artifact references.
	ArtifactRefs []ArtifactRef `json:"artifact_refs"`

	// OptionalArtifactReport (P1.2) is the audit sidecar for every
	// optional artifact the job declared (or every artifact with
	// Requirement == Optional from the cross-reference fallback).
	// One record per optional artifact, regardless of outcome.
	// The JobFinalizer also persists a JSON-encoded copy of this
	// slice into the `optional_artifact_report` job_events row
	// inside the SAME transaction (next to job_completed).
	OptionalArtifactReport []OptionalArtifactRecord `json:"optional_artifact_report,omitempty"`
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
