// Package job — finalize_commands.go: canonical typed-narrow command and
// result surface for the consolidated FinalizeAttempt store primitive
// (Fase 4(a), July 2026).
//
// Per godlike/06 SSOT (one canonical writer per fact), exactly one
// path may write terminal state transitions out of {SUCCEEDED, FAILED,
// RETRY_WAIT}. Fase 4(a) collapses the four sibling pre-FASE-4 paths
// (Complete / Fail / ScheduleRetry — DeadLetter archive is OUT OF
// SCOPE for FinalizeAttempt because it writes a separate archive
// table; the canonical DLQ-payload snapshot travels alongside the
// status transition via FinalizeAttemptCommand.DLQPayload) into that
// single canonical owner, executed as ONE SQLite transaction so the
// (jobs update + job_events insert + optional outbox emit + optional
// artifact_state patch + optional dead_letter_jobs archive) tripwire
// lands atomically.
//
// This file declares ONLY the typed surface (FinalizeAttemptOutcome
// enum, FinalizeAttemptCommand struct, FinalizeAttemptResult struct,
// ArtifactStatePatch struct, OutboxEventSpec struct). The
// store-interface surface changes land in Push 4.2; today the
// canonical kernel.Store interface still has Complete/Fail/ScheduleRetry
// alongside this typed surface so callers can migrate incrementally.
//
// godlike/02 kernel rule: this file imports only context / encoding/json
// / time; cross-package types are NOT imported here — Status, Job,
// Filter, Event remain intra-package canonical owners. The optional
// ArtifactStatePatch + OutboxEventSpec structs are typed-narrow
// mirrors of the canonical domain/artifact.ArtifactStageState and
// outboxevents.Event shapes; godlike/06 SSOT drift is caught at the
// SQL-layer fence in Push 4.5 (the SQL adapter rejects kind/state
// combinations that don't round-trip through the domain namespaces).
package job

import (
	"context"
	"encoding/json"
	"time"
)

// FinalizeAttemptOutcome is the enum of terminal decisions the
// canonical FinalizeAttempt primitive may emit.
//
// godlike/06 SSOT: the wire values align 1:1 with the canonical
// job.Status set (SUCCEEDED / FAILED / RETRY_WAIT) — no
// translation table is needed between the FinalizeAttemptOutcome
// enum and the SQL-layer Status union, so the existing CHECK
// constraint on jobs.status is reused without a new migration.
//
// godlike/07 fail-closed: FinalizeAttemptOutcome values are
// validated at the SQL-layer fence; an unknown value surfaces as
// the canonical ErrFinalizeAttemptOutcomeInvalid sentinel
// (forward-pointer, introduced in Push 4.2).
type FinalizeAttemptOutcome string

const (
	// OutcomeSucceeded — terminal success. job.status → SUCCEEDED,
	// progress=100, completed_at=now, result_json written atomically,
	// lease cleared.
	//
	// Fase 4(d) gate (forward-pointer, enforced in Push 4.5):
	// when the affected row is a parent job (parent_id NOT NULL)
	// the SQL layer REJECTS this outcome UNLESS the row's
	// parent_state_typed is ParentStateAggregating. The rejection
	// is fail-closed: callers cannot complete a parent during
	// fan-out, only when the aggregator has reported "all children
	// terminal". The gate runs BEFORE the UPDATE.
	OutcomeSucceeded FinalizeAttemptOutcome = "SUCCEEDED"

	// OutcomeFailedPermanent — terminal failure (NO retry).
	//
	// job.status → FAILED, error column populated, lease cleared.
	//
	// Fase 4 is explicit on semantics: "permanent" means
	// "no further retry from the broker scheduler's perspective".
	// Caller decides whether the failed row is also archived via
	// DLQPayload (see FinalizeAttemptCommand.DLQPayload below);
	// DLQPayload non-empty means "also write to dead_letter_jobs
	// in the SAME tx". The two decisions are intentionally
	// orthogonal so callers can SUCCEEDED-with-DLQ-snapshot,
	// FAILED-with-DLQ-snapshot, or any other combination.
	OutcomeFailedPermanent FinalizeAttemptOutcome = "FAILED_PERMANENT"

	// OutcomeScheduleRetry — non-terminal retry.
	//
	// job.status → RETRY_WAIT, retry_count += 1, error column
	// populated, lease cleared (re-claim picks up after backoff).
	//
	// Retry-limit enforcement (godlike/07 fail-closed, preserved
	// from pre-Fase-4 ScheduleRetry → Fail recursion at
	// lifecycle_aggregation.go line 28): if the resulting
	// retry_count would exceed max_retries, the SQL-layer MUST
	// atomically downgrade to OutcomeFailedPermanent instead.
	// The caller does NOT need to pre-check retry_count; the SQL
	// layer enforces the limit at the FENCE so a stale pre-check
	// (an extra retry tick) cannot corrupt the contract.
	OutcomeScheduleRetry FinalizeAttemptOutcome = "SCHEDULE_RETRY"
)

// ArtifactStatePatch is the typed-in-kernel surface for an in-tx
// artifact-state patch written by FinalizeAttempt. When the affected
// job produces an artifact (the Fase 3 artifact_stages table has a
// row linked to this job by job_id), FinalizeAttempt MAY patch the
// linked artifact row's state column IN THE SAME TX as the job
// status transition.
//
// godlike/02 kernel rule: this struct is INTRA-KERNEL; it mirrors
// the canonical internal/domain/artifact.ArtifactStageState wire-string
// without importing the domain package. The Drift between
// ArtifactStatePatch.NewState and the canonical artifact_stages.state
// CHECK constraint is enforced at the SQL-layer fence in Push 4.5
// (unknown states are rejected with ErrUnknownArtifactState;
// forwarded via the typed-out ErrArtifactStale sentinel when the
// artifact row is locked by another writer).
type ArtifactStatePatch struct {
	// ArtifactID is the canonical artifact row id (mirrors
	// internal/domain/artifact/stages.go ArtifactID field semantics).
	// Required; non-empty.
	ArtifactID string

	// NewState is the target artifact state (wire-string; matches
	// internal/domain/artifact/stages.go ArtifactStageState wire-string.
	// The canonical wire values are STAGED, PUBLISHED, SUCCEEDED,
	// FAILED_PERMANENT — see domain/artifact/stages.go for the
	// authoritative list). SQL impl in Push 4.5 enforces the
	// wire-value against the canonical artifact_stages.state
	// CHECK constraint.
	NewState string
}

// OutboxEventSpec is the typed-in-kernel surface for a single outbox
// event emitted by FinalizeAttempt in the SAME tx as the job status
// transition. The canonical outbox is at
// internal/platform/sqlite/outboxevents and its
// domain mirror at internal/platform/sqlite/outboxevents; this struct is the
// typed-narrow kernel surface that bridges the two namespaces without
// the kernel importing either.
//
// godlike/06 SSOT: the worker producing the outbox event remains the
// canonical actor; the outbox event's caller-supplied fields
// (Type, EventKey, Payload) MUST round-trip through the canonical
// outbox_events table columns verbatim. Drift between OutboxEventSpec
// and outbox_events columns is caught at the SQL-layer fence in Push
// 4.5 (e.g. an unknown Type is rejected via ErrUnknownOutboxEventType
// before the INSERT).
type OutboxEventSpec struct {
	// Type is the outbox event type (mirrors outbox_events.type
	// column wire-string). Required, non-empty. The canonical
	// enum of values lives in
	// internal/platform/sqlite/outboxevents/types.go (P1 SSOT discipline).
	Type string

	// EventKey is the idempotency key (mirrors outbox_events.event_key
	// column UNIQUE constraint). Required for idempotent replay;
	// SQL-layer fence rejects empty EventKey (the outbox's primary
	// idempotency contract).
	EventKey string

	// Payload is the typed event payload (json.RawMessage for
	// byte-stability across wire boundaries). nil indicates
	// "write '{}'".
	Payload json.RawMessage
}

// FinalizeAttemptCommand is the typed-narrow command carried to the
// canonical FinalizeAttempt primitive.
//
// godlike/06 SSOT: this struct enumerates the FULL canonical surface
// for Fase 4(a). Every user-spec field from the original push is
// declared — outcome, result, error, retry decision, next attempt,
// dlq payload, artifact state, outbox events — even if the SQL-layer
// impl in Push 4.5 processes only a subset; the exposure here locks
// in the wire contract.
//
// godlike/07 minimum-blast-radius: a single command carries the
// union of (Outcome fields + fence guards + optional payloads); the
// SQL-layer precondition check rejects nil/zero in outcome-applicable
// fields BEFORE running the UPDATE.
//
// Fence contract: (WorkerID, LeaseID, ExpectedRevision) is the
// minimal set of CAS guards for the lease-renewed window. The
// canonical lease-fence error surface (ErrLeaseLost,
// ErrTransitionConflict) is reused; the worker calling
// FinalizeAttempt MUST update its ExpectedRevision snapshot to
// the returned FinalizeAttemptResult.NewRevision before the next
// call, OR the fence will reject with ErrTransitionConflict.
type FinalizeAttemptCommand struct {
	// JobID is the canonical job identifier. Required, non-empty.
	JobID string

	// Outcome selects which terminal decision to apply. Required.
	// Unknown values are rejected as ErrFinalizeAttemptOutcomeInvalid
	// at the SQL-layer fence.
	Outcome FinalizeAttemptOutcome

	// WorkerID is the worker currently owning the lease. Required.
	// Must match jobs.worker_id at SQL-layer fence (ErrLeaseLost on
	// mismatch).
	WorkerID string

	// LeaseID is the canonical lease token. Required.
	// Must match jobs.lease_id at SQL-layer fence (ErrLeaseLost on
	// mismatch).
	LeaseID string

	// ExpectedRevision is the revision snapshot the caller
	// observed when it claimed the lease. Required.
	// Must match jobs.revision at SQL-layer fence
	// (ErrTransitionConflict on mismatch).
	//
	// Pre-Fase-4 callers had to thread expectedRevision through
	// appjobs.{CompleteCommand, FailCommand, ...} separately for
	// each method; the consolidated command carries it once.
	ExpectedRevision int

	// Result is the terminal result payload written to result_json.
	//
	// Required iff Outcome == OutcomeSucceeded; ignored (not loaded)
	// for OutcomeFailedPermanent and OutcomeScheduleRetry.
	// Nil/empty for OutcomeSucceeded is a SQL-layer precondition
	// rejection (silent-null would corrupt wire consistency on
	// downstream reads; the SQL harness writes "{}" for missing
	// payloads as a debug aid, NOT as a contract).
	Result json.RawMessage

	// ErrorMessage is the human-readable error string written to
	// jobs.error AND job_events.message.
	//
	// Required iff Outcome ∈ {OutcomeFailedPermanent, OutcomeScheduleRetry};
	// ignored for OutcomeSucceeded. Empty for those outcomes is a
	// SQL-layer precondition rejection (a transient error name
	// with an empty message is a hostile defence-in-depth trap).
	ErrorMessage string

	// Backoff is the post-OutcomeScheduleRetry re-claim delay.
	//
	// The SQL layer writes the backoff hint into the row's
	// completed_at column as a far-future timestamp IF AND ONLY
	// IF Outcome == OutcomeScheduleRetry (matches the pre-Fase-4
	// implicit backoff pattern via lease_expiry at
	// lifecycle_aggregation.go). Zero backoff = "no delay"
	// (re-claim Eligible immediately); non-zero backoff =
	// "postpone re-claim until Backoff has elapsed since now".
	//
	// Ignored for OutcomeSucceeded and OutcomeFailedPermanent.
	Backoff time.Duration

	// DLQPayload is the snapshot payload written to
	// dead_letter_jobs IF AND ONLY IF non-nil. The contract:
	// nil = "no DLQ archive this tx"; non-nil = "write to
	// dead_letter_jobs alongside the status transition, atomically".
	//
	// The canonical dead_letter_jobs row schema is (job_id,
	// job_type, correlation_id, error, payload_json, retry_count,
	// failed_at); DLQPayload is written to the payload_json
	// column verbatim (bytes-stable across wire boundaries). The
	// canonical Payload schema is forward-pointer to Push 4.5's
	// specification — today non-nil DLQPayload is the surface
	// contract, semantically-roundtripped values are TBD.
	DLQPayload json.RawMessage

	// ArtifactState is an optional in-tx artifact-state patch.
	// nil = "no artifact-state transition this tx"; non-nil =
	// "patch the artifact_stages row linked to this job_id in
	// the SAME tx, atomically".
	//
	// Implementation note: the SQL impl MUST verify that the
	// ArtifactID exists in artifact_stages AND is linked to this
	// job via artifact_stages.job_id; mismatches surface as
	// ErrArtifactStale (godlike/07 fail-closed).
	ArtifactState *ArtifactStatePatch

	// OutboxEvents is the list of outbox events emitted by this
	// FinalizeAttempt in the SAME tx. Empty = "no outbox events";
	// non-empty = "emit each event with SQL-layer IdempotencyKey
	// guard via outbox_events.event_key UNIQUE constraint".
	//
	// godlike/07 minimum-blast-radius: duplicate EventKeys within
	// the same FinalizeAttempt command are a precondition
	// rejection, NOT a silent-collapse (silent collapse would
	// prevent operators from detecting package-level bugs that
	// accidentally emit the same event twice).
	OutboxEvents []OutboxEventSpec

	// EventType is the optional job_events.type recorded in the
	// SAME transaction as the jobs row update. Empty = no event
	// written (silent-default). Forwards-compatible: future
	// EventType values can be added without schema migration
	// because the job_events.type column has no CHECK constraint.
	//
	// Convention: callers set this to "job_completed" /
	// "job_failed" / "job_retry_wait" to mirror the pre-Fase-4
	// lifecycle event types; setting it to a custom value is
	// permitted for domain-specific events (e.g. "job_aggregate_completed"
	// for phase 2 aggregators).
	//
	// NOTE: EventType + EventData are produced IN ADDITION TO
	// OutboxEvents; EventType writes to job_events (audit trail)
	// whereas OutboxEvents writes to outbox_events (downstream
	// fanout). Their domains do NOT overlap.
	EventType string

	// EventData is the optional job_events.data_json payload.
	// Used only when EventType is non-empty. nil indicates
	// "write {}"; maps are JSON-encoded by the SQL layer.
	EventData map[string]any
}

// FinalizeAttemptResult is the typed-narrow return from
// FinalizeAttempt.
//
// godlike/06 SSOT (one canonical surface per fact): every field
// is the canonical post-commit projection; callers MUST NOT
// re-query the jobs row to "double-check" — the returned struct
// IS the source of truth for the post-commit state.
//
// godlike/07 fail-closed: every field is REQUIRED for the caller
// to react correctly. FinalStatus is needed to decide whether to
// emit downstream effects (e.g. aggregator notification);
// NewRevision is needed to update the lease-fence snapshot for
// any subsequent call.
type FinalizeAttemptResult struct {
	// JobID is echoed from the command (defensive — when callers
	// do parallel batches they MUST be able to correlate return
	// values with commands).
	JobID string

	// FinalStatus is the new job.Status after the transaction
	// commits. For OutcomeScheduleRetry this is StatusRetryWait;
	// for OutcomeFailedPermanent this is StatusFailed; for
	// OutcomeSucceeded this is StatusSucceeded. When the SQL-layer
	// retry-exhaustion downgrade fires (OutcomeScheduleRetry with
	// retry_count already at max_retries), FinalStatus reflects
	// the post-downgrade state (StatusFailed) and not the
	// caller-supplied outcome.
	FinalStatus Status

	// NewRevision is the post-commit revision. The worker's
	// expectedRevision on subsequent calls MUST be updated to
	// this value before the next call, OR the lease-fence
	// will reject. Mirrors the pre-Fase-4 invariant that
	// revision is bumped on every fenced state transition.
	NewRevision int

	// DLQRecorded is true iff the SQL-layer wrote a row to
	// dead_letter_jobs in the SAME transaction. The caller
	// does NOT need to re-query dead_letter_jobs to verify;
	// this boolean is the canonical post-commit projection.
	// Always false in current ops because DLQ archiving is a
	// separate post-Finalize step (kept distinct from terminal
	// decisions per godlike/07 minimum-blast-radius).
	// Forward-pointer for future Fase 5 expansion: a single
	// call to FinalizeAttempt with DLQPayload=non-nil bumps
	// this to true atomically.
	DLQRecorded bool

	// OutboxEventsWritten is the list of outbox_event IDs emitted
	// by the transaction in the SAME atomic commit. Empty for
	// self-contained job rows (no outbox fan-out); non-empty
	// when the command's OutboxEvents slice was non-empty AND
	// each insert succeeded.
	//
	// godlike/06 SSOT: the IDs are the canonical outbox_events.id
	// values (the SQL-layer generates `evt_<unix_nano>_<hex>`
	// strings, mirroring the pre-Fase-4 job_events pattern).
	// Callers MUST NOT re-query the outbox to "verify" — these
	// IDs are the source of truth.
	OutboxEventsWritten []string
}

// FinalizeAttemptFn is the typed signature of the canonical
// FinalizeAttempt primitive. Matches the kernel.Store.FinalizeAttempt
// signature once the interface is updated in Push 4.2.
//
// godlike/06 SSOT: this faithful signature mirror is the canonical
// declaration site; the surface in Push 4.2's interface change MUST
// use this exact signature (no field-name changes; no added
// parameters; no removed parameters). Drift between the typedef and
// the interface declaration is a build-failure at the adapter's
// compile-time assertion `var _ Store = (*Adapter)(nil)`.
type FinalizeAttemptFn func(ctx context.Context, cmd FinalizeAttemptCommand) (FinalizeAttemptResult, error)

// IsValid reports whether o is one of the canonical FinalizeAttemptOutcome
// values. godlike/07 fail-closed: unknown wire values are rejected at the
// SQL-layer fence with ErrFinalizeAttemptOutcomeInvalid; this Go-side
// helper is the first line of defence (compile-time + runtime, no
// round-trip through the SQL adapter required for callers and tests).
func (o FinalizeAttemptOutcome) IsValid() bool {
	switch o {
	case OutcomeSucceeded, OutcomeFailedPermanent, OutcomeScheduleRetry:
		return true
	}
	return false
}
