// Package artifact — stages.go (FASE 3 Spina Dorsale, July 2026).
//
// Canonical domain types for the FASE 3 publication saga:
// ArtifactStage is the per-publication record backed by the
// `artifact_stages` SQLite table (migration 147). The state
// machine is intentionally distinct from the existing PR3
// `artifacts` table (internal/application/assets/artifacts/):
//
//	FASE 3 (new):     STAGED → PUBLISHED → SUCCEEDED
//	                               ↓
//	                       FAILED_PERMANENT
//	PR3 (existing):  STAGING  →  VERIFYING  →  STAGED/READY
//	                                            ↓
//	                                    FAILED/QUARANTINED
//
// The two tables coexist on the same DB; godlike/06 SSOT preserves
// the boundary (no cross-write, no schema merge). Future
// consolidation is deferred to a dedicated PR.
//
// godlike/06 SSOT: this package is the SOLE canonical owner of
// `ArtifactStage`, `ArtifactStageState`, `Requirement`, and the
// typed error sentinels. Repository implementations in
// `internal/infrastructure/database/sqlite/artifact_stages/` and
// the application service in
// `internal/application/staging/` consume ONLY the types
// declared here.
//
// godlike/07 NO-FAKE-AVAILABILITY: a bogus state or requirement
// value (one not in the canonical set) is rejected by the
// repository with `ErrInvalidArtifactStageState` /
// `ErrInvalidRequirement`, not silently accepted. Required
// artifacts that go MISSING during the saga are surfaced as
// `ErrArtifactRequiredMissing` (godlike/07 fail-fast-at-input:
// required artifact missing is a HARD fail, never a warning).
package artifact

import (
	"errors"
	"time"
)

// ── ArtifactStageState ──────────────────────────────────────────────

// ArtifactStageState is the canonical lifecycle state of a single
// FASE 3 publication record. Transitions:
//
//	STAGED          — durable staging committed (file on disk + hash
//	                  verified + row INSERT + outbox commit in same TX).
//	                  This is the initial state after Stage.Verify().
//	PUBLISHED       — publisher worker drained the outbox event,
//	                  uploaded the artifact to its `destination`,
//	                  and recorded the PublishedLocation. Set by
//	                  `artifact_publisher` worker pool on successful
//	                  Drive upload.
//	SUCCEEDED       — finalizer verified all `required` artifacts
//	                  for the job are in PUBLISHED state, and the
//	                  job is otherwise terminal-ready. Finalizer flips
//	                  the state after the FASE 3 (d) "verify all
//	                  PUBLISHED → SUCCEEDED" check.
//	FAILED_PERMANENT — terminal error from publisher worker (Drive
//	                  upload exhausted retries, hash mismatch on
//	                  re-read, schema drift); recoverable only via
//	                  manual operator intervention or a future
//	                  `retry()` command (forward-pointer, NOT in
//	                  FASE 3 scope).
type ArtifactStageState string

const (
	StateStaged          ArtifactStageState = "STAGED"
	StatePublished       ArtifactStageState = "PUBLISHED"
	StateSucceeded       ArtifactStageState = "SUCCEEDED"
	StateFailedPermanent ArtifactStageState = "FAILED_PERMANENT"
)

// IsValid reports whether st is a known ArtifactStageState.
func (st ArtifactStageState) IsValid() bool {
	switch st {
	case StateStaged, StatePublished, StateSucceeded, StateFailedPermanent:
		return true
	}
	return false
}

// String returns the canonical string representation.
func (st ArtifactStageState) String() string { return string(st) }

// IsTerminal reports whether st is a terminal state (no further
// transitions allowed). Used by the publisher worker's lease-fenced
// Mark* methods to reject stale transitions.
func (st ArtifactStageState) IsTerminal() bool {
	switch st {
	case StateSucceeded, StateFailedPermanent:
		return true
	}
	return false
}

// ── Requirement ──────────────────────────────────────────────────────

// Requirement is the canonical "is this artifact required for the
// job to SUCCEED?" policy. FASE 3 (b): "Artifact richiesto mancante
// ⇒ errore, mai warning." A `required` artifact in FAILED_PERMANENT
// state forces the job to FAILED state (it does NOT grace-
// degrade to SUCCEEDED_WITH_WARNINGS).
type Requirement string

const (
	// RequirementOptional means missing or failed is acceptable —
	// the job may still SUCCEED without this artifact.
	RequirementOptional Requirement = "optional"
	// RequirementRequired means the saga MUST end with this artifact
	// in PUBLISHED state for the job to SUCCEED. Missing →
	// ERR_ARTIFACT_REQUIRED_MISSING. FAILED_PERMANENT →
	// job.Failed("missing_required_artifact").
	RequirementRequired Requirement = "required"
)

// IsValid reports whether r is a known Requirement.
func (r Requirement) IsValid() bool {
	switch r {
	case RequirementOptional, RequirementRequired:
		return true
	}
	return false
}

// String returns the canonical string representation.
func (r Requirement) String() string { return string(r) }

// ── ArtifactStage ───────────────────────────────────────────────────

// ArtifactStage is the canonical per-publication record. It is the
// authoritative source of truth for the FASE 3 saga's contract:
// "did this job's artifacts get safely staged + uploaded + finalised?"
//
// godlike/06 SSOT: this struct shape is the canonical mirror of the
// `artifact_stages` SQLite table (migration 147). Adding a column is
// a 3-surface lockstep change: this struct + the migration + the
// repository scan helpers.
type ArtifactStage struct {
	// ID is the canonical primary key (TEXT, NOT NULL). Generated by
	// the staging service as `art_<unix_nano>_<8hex>` (mirrors the
	// canonical ID pattern from internal/infrastructure/database/sqlite/operations/repository.go).
	ID string

	// JobID is the canonical job this publication belongs to.
	// FK-by-convention to `jobs.id` (canonical `job_<...>` pattern).
	// NOT enforced at the DB level (sqlite_jobs table is defined
	// separately; cross-DB FK enforcement requires cross-table wiring
	// at the application layer).
	JobID string

	// LocalPath is the canonical absolute filesystem path to the
	// staged file (under the staging workspace — typically
	// /var/lib/pipelinegen/staging/{job_id}/{artifact_id}).
	LocalPath string

	// Hash is the canonical 64-char lowercase hex SHA-256 of the
	// staged file's content. Computed DURING write via the
	// io.MultiWriter pattern (FASE 3 (a) "hash during write") — not
	// via a separate post-write read.
	Hash string

	// Size is the artifact size in bytes (canonical i64). 0 is an
	// invalid value (an empty staged file is rejected by
	// staging.Store before INSERT).
	Size int64

	// Mime is the canonical IANA media type (e.g. "audio/mpeg",
	// "image/png", "video/mp4"). Validated against the IANA registry
	// format `type/subtype` at the staging-service input boundary.
	Mime string

	// Requirement is the per-artifact "required vs optional" policy
	// (FASE 3 (b)). Default is `optional` (the conservative default
	// for backward compat with PR3 artifacts).
	Requirement Requirement

	// Destination is the canonical delivery destination (mirrors the
	// delivery.DestinationKey shape). Used by the publisher worker
	// to resolve the Drive folder + retry policy at upload time.
	Destination string

	// State is the lifecycle state (see ArtifactStageState constants).
	State ArtifactStageState

	// AttemptCount is the publisher worker retry counter. Increments
	// on each MarkFailed transition; cleared on MarkPublished. NOT
	// incremented on repository validateForWrite errors (those are
	// pre-attempt — the row hasn't been claimed yet).
	AttemptCount int

	// LastError is the terminal error string on FAILED_PERMANENT.
	// Empty on all other states. Cleared on any successful Mark*.
	LastError string

	// PublishedLocation is the JSON-serialised delivery.PublishedLocation
	// (kind + uri + external_id) populated when State → PUBLISHED.
	// Format: compact JSON, e.g.
	// `{"kind":"drive","uri":"drive-file-id","external_id":"1abc..."}`.
	PublishedLocation string

	// PublishedAt is the RFC3339 timestamp at the state → PUBLISHED
	// transition. NULL on STAGED + terminal states.
	PublishedAt *time.Time

	// CreatedAt is the RFC3339 timestamp at StageVerify INSERT.
	CreatedAt time.Time

	// UpdatedAt is the RFC3339 timestamp of the most recent state
	// transition. Initial value equals CreatedAt.
	UpdatedAt time.Time
}

// ── Typed error sentinels ───────────────────────────────────────────

// ErrInvalidArtifactStageState is returned by the repository when a
// caller writes an out-of-set ArtifactStageState value.
// godlike/07 fail-closed: bogus state is rejected, not silently
// accepted (NO-FAKE-AVAILABILITY).
var ErrInvalidArtifactStageState = errors.New("artifact_stages: invalid state (not in canonical ArtifactStageState set)")

// ErrInvalidRequirement is returned by the repository when a caller
// writes an out-of-set Requirement value.
var ErrInvalidRequirement = errors.New("artifact_stages: invalid requirement (not in canonical Requirement set)")

// ErrArtifactStageNotFound is returned by Repository.GetByID when no
// row matches the given id. The HTTP layer maps it to 404.
var ErrArtifactStageNotFound = errors.New("artifact_stages: stage not found")

// ErrArtifactRequiredMissing is returned by the finalizer when at
// least one REQUIRED artifact for a job is MISSING (no rows) or in
// FAILED_PERMANENT state. FASE 3 (b) fail-closed: required artifact
// missing is a HARD error. Caller maps to job=failed + operator alert.
var ErrArtifactRequiredMissing = errors.New("artifact_stages: required artifact missing or in FAILED_PERMANENT state (FASE 3 (b) fail-closed)")

// ErrQuotaExceeded is returned by the staging.Store when an inbound
// write would exceed the per-artifact quota (default 10GB) or the
// workspace total (default 100GB). FASE 3 (a) "quota/disk check".
var ErrQuotaExceeded = errors.New("artifact_stages: quota exceeded (per-artifact or workspace total)")

// ErrDiskSpaceLow is returned by the staging.Store when the workspace's
// free disk space drops below the configured minimum (default 1GB).
// FASE 3 (a) "quota/disk check".
var ErrDiskSpaceLow = errors.New("artifact_stages: free disk space below minimum")

// ErrArtifactStageHashMismatch is returned by the staging.Store when
// the hash computed during write does not match a caller-supplied
// expected hash. Forward-pointer use case: callers MAY supply an
// expected hash to detect corrupted uploads (the canonical "trust
// local file system" assumption is replaced with a per-write hash
// verification).
var ErrArtifactStageHashMismatch = errors.New("artifact_stages: hash mismatch during write (caller expected hash differs from computed hash)")

// ErrArtifactStageIDCollision is returned by the staging.Store when
// the requested ID is already in the local-side staging map AND the
// existing hash differs from the new request's computed hash.
// Resolution: caller MUST supply a fresh ID. This is a HARD error
// (NOT a re-stage) because a hash mismatch on the same ID is a
// corruption signal.
var ErrArtifactStageIDCollision = errors.New("artifact_stages: ID collision (same ID, different hash — corruption signal)")

// ErrArtifactStageEmpty is returned by the staging.Store when the
// inbound write produces a 0-byte file (size==0). Forward-pointer
// for "empty artifact is invalid".
var ErrArtifactStageEmpty = errors.New("artifact_stages: empty artifact (0 bytes — caller must supply non-empty content)")

// WrapArtifactStageNotFound attaches the missing stage id to
// ErrArtifactStageNotFound for operator-audit logging. The returned
// error wraps the canonical sentinel so errors.Is probes still
// succeed.
func WrapArtifactStageNotFound(id string) error {
	return Wrap(ErrArtifactStageNotFound, "id=%s", id)
}

// WrapArtifactRequiredMissing attaches the missing artifact id + job
// id to ErrArtifactRequiredMissing for operator-audit logging.
func WrapArtifactRequiredMissing(jobID, requirement, id string) error {
	return Wrap(ErrArtifactRequiredMissing, "job_id=%s requirement=%s id=%s", jobID, requirement, id)
}

// Wrap is a small typed-error helper that preserves the canonical
// sentinel (errors.Is(outer, sentinel) == true) while attaching
// operator-audit context. Defined locally to avoid the import dance
// across packages; mirrors the `Wrap` helper pattern in
// internal/domain/operations/types.go.
func Wrap(sentinel error, format string, args ...any) error {
	return wrappedError{sentinel: sentinel, msg: fmt.Sprintf(format, args...)}
}

// wrappedError carries a canonical sentinel + a formatted audit
// string. Implements errors.Is + errors.Unwrap for typed-error probes.
type wrappedError struct {
	sentinel error
	msg      string
}

func (e wrappedError) Error() string { return e.sentinel.Error() + ": " + e.msg }
func (e wrappedError) Unwrap() error { return e.sentinel }

// Unused `time` import guard — kept explicit so the linter does not
// add a "unused import" annotation when the file is edited to
// drop the Time field temporarily.
var _ = time.Time{}
