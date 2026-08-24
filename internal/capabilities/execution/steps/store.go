// Package steps defines the canonical port for the resumable step store
// (PipelineGen Stock Cutover §12-3, July 2026).
//
// godlike/06 SSOT: this package is the single owner of the "did phase X
// run/complete/fail for job Y with input fingerprint Z" fact. Prior
// scattered local maps that tracked phase state are forward-pointer
// migration targets — call sites should consult Store as the only
// authoritative source.
//
// godlike/07 typed-error contract: all errors returned by Store are
// sentinel `errors.New(...)` declared in this file. Callers can
// `errors.Is(err, ErrStepAlreadyCompleted)` from any seam without
// walking an opaque string-chain.
//
// Design A (per-row canonical): each (jobID, stepKey, fingerprint) is a
// distinct row. Retries with the same fingerprint are idempotent (the
// existing row's attempt counter bumps + status resets, OR no-op if
// status=completed). Retries with a different fingerprint INSERT a new
// row, preserving the audit trail of fingerprint-version attempts.
// "First non-completed phase" semantics look at the LATEST row per
// (jobID, stepKey) and find the first one whose status != completed,
// ordered by stepKey ASC. This requires step_keys to use a lexically
// sortable naming convention (e.g., "01_stage", "02_render", "03_upload",
// "04_index"); a plain alphabetical name would sort incorrectly
// ("cut", "publish", "render" instead of "cut", "render", "publish").
package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// StepStatus is the closed 4-state machine for a single step's run.
//
// The 4 states form a forward DAG plus one terminal sink:
//
//	(none) ─MarkStarted─> Pending ─MarkStarted─> Running
//	                                       │
//	                                       ├──MarkCompleted─> Completed (terminal sink)
//	                                       │
//	                                       └──MarkFailed───> Failed ─MarkStarted─> ...
//
// ANY Mark* call on a Completed row returns ErrStepAlreadyCompleted
// (godlike/07 fail-closed semantics: terminal state is immutable).
type StepStatus string

const (
	StatusPending   StepStatus = "pending"
	StatusRunning   StepStatus = "running"
	StatusCompleted StepStatus = "completed"
	StatusFailed    StepStatus = "failed"
)

// CanonicalStepStatusValues returns the closed 4-state set in canonical
// (declaration) order. Use for iteration, JSON marshalling canonical
// ordering, and tests that pin the closed set.
func CanonicalStepStatusValues() []StepStatus {
	return []StepStatus{StatusPending, StatusRunning, StatusCompleted, StatusFailed}
}

// IsValid returns true iff s is one of the 4 canonical closed values.
// Mirrors the pattern at internal/kernel/asset/asset_types.go::LifecycleState.
func (s StepStatus) IsValid() bool {
	switch s {
	case StatusPending, StatusRunning, StatusCompleted, StatusFailed:
		return true
	}
	return false
}

// Sentinel errors. All `errors.New(...)`; reachable via `errors.Is` from
// any caller seam (godlike/07 typed-error contract).
var (
	// ErrStoreNotWired is returned by Store consumers when the
	// composition root injects a nil Store. The message names the
	// composition-root wiring site so operators can locate the missing
	// dep without unwrapping.
	ErrStoreNotWired = errors.New("steps.Store: composition root did not wire a Store (godlike/05 fail-closed)")

	// ErrStepAlreadyCompleted is returned by Mark* when the existing
	// row's status is Completed (terminal sink). This is the
	// canonical godlike/07 no-fake-availability gate: a completed
	// step cannot be restarted, failed, or re-completed. Any caller
	// that wants to "redo" a completed step must use a NEW
	// inputFingerprint so a fresh row is INSERTed (the prior row
	// stays as audit-trail history).
	ErrStepAlreadyCompleted = errors.New("steps: step already completed (terminal sink; use a new InputFingerprint to restart)")

	// ErrStepNotFound is returned by MarkCompleted / MarkFailed when
	// no row exists for the (jobID, stepKey, inputFingerprint) triple.
	// Pre-MarkStarted completion is a programming error; the port
	// surfaces it loudly rather than silently INSERT+a-completed-row.
	ErrStepNotFound = errors.New("steps: no row for (jobID, stepKey, inputFingerprint) triple (call MarkStarted first)")

	// ErrInvalidStepKey is returned by validation helpers when a
	// StepKey has an empty JobID, StepKey, or InputFingerprint.
	// Surfaced by Validated() aggregations, never directly from SQL.
	ErrInvalidStepKey = errors.New("steps: StepKey missing JobID / StepKey / InputFingerprint (validated by StepKey.Validated)")
)

// StepKey is the canonical triple identifying a single step attempt.
// Re-used across Store methods to keep the surface narrow.
type StepKey struct {
	JobID            string `json:"job_id"`
	StepKey          string `json:"step_key"`
	InputFingerprint string `json:"input_fingerprint"`
}

// Validated returns nil iff JobID / StepKey / InputFingerprint are all
// non-empty. Aggregated into ONE error message listing ALL missing
// fields (godlike/07 no-fake-availability).
func (s StepKey) Validated() error {
	var missing []string
	if s.JobID == "" {
		missing = append(missing, "JobID")
	}
	if s.StepKey == "" {
		missing = append(missing, "StepKey")
	}
	if s.InputFingerprint == "" {
		missing = append(missing, "InputFingerprint")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: missing %v", ErrInvalidStepKey, missing)
	}
	return nil
}

// StepState is the canonical typed envelope for a single row in the
// execution_steps table. JSON columns (Result / ArtifactRefs) are
// returned as json.RawMessage so callers can decode the payload with
// their domain-specific schema (the port stays out of the payload
// shape — godlike/06 SSOT one-typed-owner-per-fact).
type StepState struct {
	ID           int64           `json:"id"`
	JobID        string          `json:"job_id"`
	StepKey      string          `json:"step_key"`
	Fingerprint  string          `json:"input_fingerprint"`
	Status       StepStatus      `json:"status"`
	Attempt      int             `json:"attempt"`
	Result       json.RawMessage `json:"result_json"`
	ArtifactRefs json.RawMessage `json:"artifact_refs_json"`
	StartedAt    time.Time       `json:"started_at"`
	CompletedAt  time.Time       `json:"completed_at"`
	LastError    string          `json:"last_error"`
}

// Store is the canonical typed port. Pattern-0 abstraction: tests inject
// hermetic in-memory fakes (store_test.go) without dragging in SQLite
// (no `database/sql` leak above the application layer per godlike/06 SSOT).
//
// All methods are safe for concurrent use by multiple worker goroutines
// racing on the same (jobID, stepKey) — the underlying SQLite transaction
// model + UNIQUE INDEX guarantees single-writer-per-row serialization.
//
// Method semantics per Design A:
//
//   - MarkStarted is the entry-point: idempotent for matching triple,
//     but bumps `attempt` and resets status to Pending (or stays
//     Running on transition). If the existing row is Completed, returns
//     ErrStepAlreadyCompleted (terminal-immutability).
//   - MarkCompleted transitions Pending|Running|Failed → Completed,
//     stamps `CompletedAt`, and stores result + artifact refs. Setting
//     Completed when the row is already Completed returns
//     ErrStepAlreadyCompleted (re-completion is a programming error).
//   - MarkFailed transitions Pending|Running|Failed → Failed,
//     stamps `LastError`. MarkFailed on Completed returns
//     ErrStepAlreadyCompleted.
//   - FirstNonCompleted returns nil if all (per-step latest) rows are
//     Completed. Otherwise returns the canonical "first non-completed"
//     step (lowest stepKey per lexical ASC sort, latest id per
//     (jobID, stepKey) fingerprint).
//   - ListByJob returns ALL rows for the job ordered by stepKey ASC
//     (then id ASC), so callers can reconstruct the full fingerprint
//     history (not just the latest per stepKey).
type Store interface {
	// MarkStarted records that work for (jobID, stepKey) is beginning
	// with inputFingerprint. Idempotent on re-call with same triple
	// (resets attempt / status). Returns ErrStepAlreadyCompleted if
	// the existing row is Completed (terminal-immutability).
	// Returns ErrInvalidStepKey (wrapped) if any of the three fields is empty.
	MarkStarted(ctx context.Context, key StepKey) error

	// MarkCompleted transitions the row to Completed and stamps
	// result + artifact_refs + completed_at. Idempotency: if the
	// row is already Completed AND the input result+artifact_refs
	// match the prior call (byte-for-byte), returns nil (callers
	// can safely retry without re-stamping timestamps). If the row
	// is Completed AND input differs, returns ErrStepAlreadyCompleted
	// (re-completion with a different shape is a programming error
	// surfacing loudly per godlike/07).
	MarkCompleted(ctx context.Context, key StepKey, result, artifactRefs json.RawMessage) error

	// MarkFailed transitions the row to Failed and stamps LastError.
	// Returns ErrStepAlreadyCompleted if the row is Completed.
	MarkFailed(ctx context.Context, key StepKey, errMessage string) error

	// FirstNonCompleted returns the canonical first non-completed step
	// (lexically smallest stepKey whose latest row is NOT Completed).
	// Returns (nil, nil) when all latest rows are Completed. Uses a
	// single SELECT MAX(id) GROUP BY step_key subquery for O(N) scan.
	FirstNonCompleted(ctx context.Context, jobID string) (*StepState, error)

	// ListByJob returns ALL rows for jobID, ordered by stepKey ASC
	// then id ASC, so callers can reconstruct the fingerprint-version
	// audit log. Returns (nil, nil) for unseen jobID.
	ListByJob(ctx context.Context, jobID string) ([]StepState, error)
}
