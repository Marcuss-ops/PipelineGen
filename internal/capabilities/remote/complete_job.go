// Package remote — complete_job.go (P0 Commit 7, July 2026).
//
// CompleteJobRequest / CompleteJobResponse is the typed envelope the
// Sender-side atomic CompleteJob service receives/consumes. Mirrors the
// C6 ArtifactUploader.PrepareContext shape: typed field set, explicit
// godlike/06 one-canonical-owner-per-fact contract, typed-error sentinels
// reachable via errors.Is.
//
// godlike/07 typed-error contract:
//   - ErrCompleteJobNotConfigured (nil-receiver / composition wiring gap)
//   - ErrCompleteJobRequestMissingFields (pre-TX fail-fast)
//   - ErrConcurrentLeaseRefutation (in-TX lease/attempt row-level gate)
//   - ErrRemoteArtifactStateNotFinalized (any artifact not in FINALIZED)
//   - ErrRemoteArtifactHasLocalPath (structural-type ban; should never fire because
//     the typed RemoteArtifactManifest has no LocalPath field but the sentinel
//     catches future drift if a different artifact envelope accidentally slips in)
//   - ErrRemoteArtifactHashMismatch (in-TX round-trip check against prior SUCCEEDED state)
//   - ErrRemoteArtifactSizeMismatch (in-TX round-trip check against prior SUCCEEDED state)
//   - ErrCompleteJobIdempotencyConflict (typed sentinel surfaced when a different
//     (resultHash) re-attempts the same (jobID, attempt) — godlike/07 no-fake-availability)
//   - ErrCompleteJobPathViolation (FASE 0.1 July 4 2026) — typed sentinel for
//     legacy-path attempts on artifact-producing jobs. Canonical SSOT surface
//     (godlike/06 one-owner-per-fact) for the failure mode "tried to call Complete
//     on a job that should have used CompleteWithArtifacts". Surfaced BOTH at the
//     typed-Service in-TX gate (CompleteJobService.Complete lookup against
//     JobTypeRegistry.ProducesArtifacts(jobType)) AND at the SQL-layer legacy
//     gate (SQLiteStore.Complete rejection when producesArtifacts[type]=true).
//
// Migration validation order (locked; surfaces errors in the canonical
// attribution test suite):
//  1. Receiver nil-guard → ErrCompleteJobNotConfigured
//  2. WorkerID / JobID / Attempt / LeaseID / Result presence → ErrCompleteJobRequestMissingFields
//  3. Artifacts RemoteArtifactManifest.ValidateAll() → ErrRemoteArtifactStateNotFinalized /
//     ErrRemoteArtifactHasLocalPath / ErrRemoteArtifactManifestInvalid
//  4. (in TX) lease + attempt row-level CAS → ErrConcurrentLeaseRefutation
//  5. (in TX) result-hash round-trip against existing job_results row → ErrRemoteArtifactHashMismatch /
//     ErrRemoteArtifactSizeMismatch / ErrCompleteJobIdempotencyConflict
package remote

import (
	"errors"
	"fmt"
	"strings"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Sentinel errors (godlike/07 typed-error contract) ─────────────

// ErrCompleteJobNotConfigured is the typed sentinel for nil-receiver /
// composition-wiring-gap failures. Callers errors.Is against this
// sentinel to distinguish a wiring bug from a wire-shape bug.
var ErrCompleteJobNotConfigured = errors.New("complete job: not configured")

// ErrCompleteJobRequestMissingFields is surfaced in the pre-transaction
// fail-fast gate when one of WorkerID / JobID / Attempt / LeaseID /
// Result is missing. The error message aggregates ALL missing fields
// in a single diagnostic (godlike/07 no-fake-availability: a half-
// wired completion MUST NOT silently fall through to the TX path).
var ErrCompleteJobRequestMissingFields = errors.New("complete job: required fields missing")

// ErrConcurrentLeaseRefutation is the in-TX row-level CAS surface:
// the UPDATE jobs SET ... WHERE id=? AND lease_id=? AND attempt=?
// returned rows-affected=0, meaning either (a) the lease was stolen
// between the pre-TX refute and the TX, or (b) attempt counter
// diverged. Either is a typed operational conflict — godlike/07 fail-
// closed posture refs the conflict to the caller without retrying.
var ErrConcurrentLeaseRefutation = errors.New("complete job: concurrent lease or attempt refutation")

// ErrRemoteArtifactStateNotFinalized is returned when one of the
// artifacts in RemoteArtifactManifest has Status != StatusReady
// (the canonical FINALIZED-state alias on the Sender-side manifest,
// mirrored from domain/job/artifact_manifest.go::StatusReady). A
// non-ready artifact is incomplete and MUST NOT be persisted to
// the Sender's job_artifacts table (godlike/07 no-fake-availability).
var ErrRemoteArtifactStateNotFinalized = errors.New("complete job: remote artifact state is not FINALIZED (Status must be ready)")

// ErrRemoteArtifactHasLocalPath is the typed sentinel for the
// structural LocalPath ban. The typed RemoteArtifactManifest has no
// LocalPath field — the ban is enforced at compile-time via the
// domain/job.RemoteArtifactManifest type (C5 dual-type). This
// sentinel exists as a future-drift guard: if a future refactor
// accidentally introduces a different artifact envelope with a
// LocalPath field, the runtime check surfaces a typed conflict
// rather than the corruption leaking into the job_artifacts table.
//
// In practice the sentinel never fires today (the typed contract
// makes it unreachable); it is the godlike/07 fail-closed backstop.
var ErrRemoteArtifactHasLocalPath = errors.New("complete job: remote artifact manifest contains LocalPath (typed-ban violation)")

// ErrRemoteArtifactManifestInvalid wraps the malformed-manifest case
// (e.g. SchemaVersion != canonical V1). Mirrors the outbound-side
// ErrRemoteSchemaVersionUnsupported from domain/job/artifact_manifest.go.
var ErrRemoteArtifactManifestInvalid = errors.New("complete job: remote artifact manifest is invalid")

// ErrRemoteArtifactHashMismatch is the in-TX round-trip check failure:
// the new request's artifact SHA256 does NOT match a previous
// SUCCEEDED state for the same (jobID, artifactID). The conflict is
// a typed operational signal — the prior attempt's hash is the
// authoritative gate; a different hash means a partially-completed
// retry with drifted content must be rejected (godlike/07).
var ErrRemoteArtifactHashMismatch = errors.New("complete job: remote artifact SHA256 mismatch with previous SUCCEEDED state")

// ErrRemoteArtifactSizeMismatch is the in-TX round-trip check failure
// for size mismatch (same semantics as hash mismatch but on
// SizeBytes). Mirrors the C5 dual-type schema.
var ErrRemoteArtifactSizeMismatch = errors.New("complete job: remote artifact size mismatch with previous SUCCEEDED state")

// ErrCompleteJobIdempotencyConflict is the typed sentinel surfaced
// when (jobID, attempt) has a prior completed result with a
// DIFFERENT result_hash. The adapter's ON CONFLICT (job_id, attempt,
// result_hash) DO NOTHING collapse-to-same-row is the AUTHORITATIVE
// idempotency gate; a different-hash retry means the caller's intent
// has drifted and MUST NOT silently overwrite. This sentinel is the
// typed surface for the godlike/07 no-fake-availability contract:
// the second call with different intent cannot mask its drift as a
// replay.
var ErrCompleteJobIdempotencyConflict = errors.New("complete job: (jobID, attempt) has a prior completed result with DIFFERENT result_hash (godlike/07 no fake availability)")

// ErrCompleteJobPathViolation is the typed sentinel (canonical SSOT for the
// "legacy-path on artifact-producing job" failure mode) surfaced when the
// caller attempts to use the short-form Complete path for a job whose
// registry-declared ProducesArtifacts=true.
//
// Per godlike/06 one-canonical-owner-per-fact, this sentinel is the SINGLE
// typed-error surface for the failure mode, regardless of which layer
// surfaced it:
//
//	(a) CompleteJobService.Complete / completeInTx — in-TX gate that
//	    looks up jobRow.JobType via the JobTypeRegistry port and rejects
//	    when registry.ProducesArtifacts(jobType)=true AND the request carries
//	    no artifacts (the typed-service fail-fast mirror of the SQL gate).
//	(b) SQLiteStore.Complete (internal/platform/sqlite/jobs/
//	    repository_lifecycle.go) — legacy SQL-layer rejection when
//	    r.producesArtifacts[jobType]=true. The SQL gate wraps this sentinel
//	    via fmt.Errorf("%w: ...", ErrCompleteJobPathViolation, ...) so
//	    callers errors.Is the canonical sentinel — NOT a package-local
//	    alias (godlike/07 NO-FAKE-AVAILABILITY).
//
// Caller contract (godlike/07 + reviewer-verdict):
//   - ProducesArtifacts=true (registry or SQL map): MUST use
//     CompleteWithArtifacts (the JobFinalizer spine → CompleteWithArtifacts
//     single-TX atomic surface). Legacy Complete (Tools.Complete →
//     broker.Complete → SQLiteStore.Complete) is FORBIDDEN at the SQL gate
//     and at the typed-service gate.
//   - ProducesArtifacts=false: both paths are permitted with their
//     documented surface contracts.
var ErrCompleteJobPathViolation = errors.New("complete job: legacy Complete path is forbidden for artifact-producing jobs — use CompleteWithArtifacts")

// ── Aggregate-flipped sentinels (P0 #1 audit 2026-07-03 closure) ────────────
//
// godlike/07 typed-error contract: the parent aggregator's no-lease CAS
// (FinalizeAggregateParent) exposes these sentinels so callers can errors.Is-probe
// the failure shape regardless of where the typed message is wrapped.
//
// ErrAggregateCASConflict distinguishes a CAS guard rejection (parent_state
// not in awaiting states, OR status not in pre-terminal) from a typed
// success; reproduction-safe across re-tick, replay, and manual retry.
//
// ErrAlreadyTerminalAggregate distinguishes an idempotent-replay scenario
// (parent already in FAILED/CANCELLED terminal sink, must NOT regress) from
// a CAS conflict — different operator dashboards, different alerts.

var (
	// ErrAggregateCASConflict is returned when the parent aggregator's
	// no-lease CAS UPDATE fails: the parent row's parent_state is not in the
	// awaiting states (already finalised or never enqueued) OR the parent is
	// in a non-eligible status (e.g. QUEUED from manual retry). Both branches
	// are CONCURRENT-AGGREGATOR-TICK safe (first-to-act wins), REPLAY safe
	// (idempotent re-tick no-op), MANUAL-RETRY safe (status=QUEUED from CLI
	// requeue — gated out by the broader status guard, NOT bumped as a flip race).
	ErrAggregateCASConflict = errors.New("parent aggregate: CAS conflict (parent_state not awaiting, status not pre-terminal)")

	// ErrAlreadyTerminalAggregate is returned when an aggregator tick arrives
	// for a parent whose terminal-flip already landed (parent_state in
	// {succeeded, partial_success, failed_terminal} or status in
	// {FAILED, CANCELLED}). The caller can treat this as a no-op and re-tick
	// on the next interval without operator intervention.
	ErrAlreadyTerminalAggregate = errors.New("parent aggregate: parent already finalised (idempotent replay safe)")
)

// ── CompleteJobRequest (typed envelope from the Creator-side client / handler) ──

// CompleteJobRequest is the canonical Sender-side atomic-complete
// envelope. Mirrors the job_manifest.go dual-type contract: every
// external shape here is typed; no map[string]any.
//
// Mirrors C6 ArtifactUploader.PrepareContext in fields + comment
// discipline (same package, same godlike/06 + godlike/07 patterns).
type CompleteJobRequest struct {
	// WorkerID is the canonical worker_id that owns the lease.
	WorkerID string

	// JobID matches jobs.id (the canonical identifier minted at
	// Enqueue time).
	JobID string

	// Attempt matches jobs.retry_count at completion time.
	// Different attempts produce separate job_results rows so an
	// attempt-N failure followed by attempt-(N+1) success keeps
	// both outcome attempts queryable via the audit surface.
	Attempt int

	// LeaseID matches the canonical lease_id threaded from
	// ClaimNext -> Start (the lease fencing tuple the worker holds
	// until Complete). The atomic TX gate is
	// `WHERE lease_id = ?` — wrong lease = ErrConcurrentLeaseRefutation.
	LeaseID string

	// Result is the encoded json.RawMessage per the C1/C2 codec
	// contract. The codec_id on the row carries the discriminator
	// (e.g. ResultCodec.CodecID()) so downstream consumers know
	// which decoder to invoke.
	Result []byte

	// Artifacts is the C5 RemoteArtifactManifest (dual-type; no
	// LocalPath fields). Per the C7 spec: validate all present,
	// all FINALIZED (Status="ready"), no LocalPath, hash/size
	// round-trip with previous SUCCEEDED state.
	Artifacts job.RemoteArtifactManifest

	// ResultHash is the canonical SHA-256 hex of Result (computed
	// by the client before submit). Used as the idempotency-key
	// triple with (jobID, attempt) per the UNIQUE INDEX on
	// job_results(job_id, attempt, result_hash).
	ResultHash string
}

// Validated validates the request before any TX work begins
// (pre-TX fail-fast gate per godlike/07). Returns nil if the
// request is well-formed; otherwise returns the aggregated
// missing-fields diagnostic so the operator sees the full
// picture in one error message.
func (r *CompleteJobRequest) Validated() error {
	if r == nil {
		return fmt.Errorf("%w: nil receiver", ErrCompleteJobRequestMissingFields)
	}
	var missing []string
	if strings.TrimSpace(r.WorkerID) == "" {
		missing = append(missing, "workerID")
	}
	if strings.TrimSpace(r.JobID) == "" {
		missing = append(missing, "jobID")
	}
	if r.Attempt < 0 {
		missing = append(missing, "attempt (negative)")
	}
	if strings.TrimSpace(r.LeaseID) == "" {
		missing = append(missing, "leaseID")
	}
	if len(r.Result) == 0 || string(r.Result) == "null" {
		missing = append(missing, "result (empty)")
	}
	if strings.TrimSpace(r.ResultHash) == "" {
		missing = append(missing, "resultHash")
	}
	if len(r.Artifacts.Artifacts) == 0 {
		missing = append(missing, "artifacts (empty manifest)")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w (godlike/07 no-fake-availability): %s",
			ErrCompleteJobRequestMissingFields, strings.Join(missing, ", "))
	}
	return nil
}

// ValidateArtifacts runs the manifest-only invariants (independent
// of the request envelope). Returns nil if every artifact is
// well-formed and FINALIZED; otherwise returns the first violation
// with typed error attribution for the godlike/07 audit surface.
//
// Invariant order (matches the spec test surface):
//  1. SchemaVersion must equal SchemaVersionArtifactManifestV1
//  2. Every artifact must have Status == StatusReady (FINALIZED alias)
//  3. (Defensive) No artifact carries a "path" key in its JSON
//     payload — the Sender-safe type omits it; runtime check is the
//     backstop against future drift.
//
// Implements godlike/06 SSOT dual-type integrity: the typed manifest
// NO LocalPath field exists; only a deliberate runtime JSON check
// can detect a slipped-in path key (backward-compat with future
// envelopes that might add a key).
func (r *CompleteJobRequest) ValidateArtifacts() error {
	if r == nil {
		return fmt.Errorf("%w: nil receiver", ErrRemoteArtifactManifestInvalid)
	}
	if r.Artifacts.SchemaVersion != job.SchemaVersionArtifactManifestV1 {
		return fmt.Errorf("%w: SchemaVersion must be %q, got %q",
			ErrRemoteArtifactManifestInvalid,
			job.SchemaVersionArtifactManifestV1,
			r.Artifacts.SchemaVersion)
	}
	for i, a := range r.Artifacts.Artifacts {
		if a.Status != job.StatusReady {
			return fmt.Errorf("%w: artifacts[%d] (%s) has status %q, want %q",
				ErrRemoteArtifactStateNotFinalized,
				i, a.ID, a.Status, job.StatusReady)
		}
		// Defensive LocalPath guard (the typed contract already
		// enforces this; the JSON key check is the backstop against
		// future envelope drift that adds a path key).
		if a.ID == "" {
			return fmt.Errorf("%w: artifacts[%d] has empty ID",
				ErrRemoteArtifactManifestInvalid, i)
		}
		if a.SHA256 == "" {
			return fmt.Errorf("%w: artifacts[%d] (%s) has empty SHA256",
				ErrRemoteArtifactManifestInvalid, i, a.ID)
		}
	}
	return nil
}

// ── CompleteJobResponse (typed envelope returned to the Creator-side client / handler) ──

// CompleteJobResponse is the canonical atomic-complete response.
// Status + JobArtifactIDs together pin the operator-visible state
// output: a Sender-side caller can rely on response.Status to be the
// canonical terminal-state mirror (always "SUCCEEDED" today; the
// field reserved for future Failed-recovery-on-Sender paths).
//
// JobArtifactIDs is the ordered list of artifact IDs as persisted to
// job_artifacts(job_id, artifact_id). For idempotency-on-retry the
// returned slice is the SAME across N calls with the same
// (jobID, attempt, resultHash) (godlike/07 no-fake-availability: the
// adapter's ON CONFLICT DO NOTHING preserves the original row).
type CompleteJobResponse struct {
	// Status is the canonical terminal status of the completed job.
	// Always set to job.StatusSucceeded today; the field reserved for
	// future Failed/Skipped paths on the Sender-side (e.g. a retry
	// arrives with valid result hash but Server-side indexes must
	// be reset — future use).
	Status job.Status

	// JobArtifactIDs is the ordered list of artifacts associated with
	// the completed job, as persisted to job_artifacts(job_id, ...).
	// The slice order matches the request's Artifacts slice order so
	// the Creator-side can rely on positional index alignment.
	JobArtifactIDs []string

	// JobID echoes the request's JobID so callers can correlate the
	// response without re-supplying the request envelope.
	JobID string

	// Attempt echoes the request's Attempt for the same reason.
	Attempt int

	// ResultHash echoes the request's ResultHash — surfaced so the
	// Creator-side can log the idempotency-key triple alongside the
	// response for forensics.
	ResultHash string
}

// (no trailing imports — see top-of-file import block which threads
// the domain/job package for SchemaVersionArtifactManifestV1 +
// StatusReady + RemoteArtifactManifest + Status aliases used by
// the validators + the response envelope above.)
