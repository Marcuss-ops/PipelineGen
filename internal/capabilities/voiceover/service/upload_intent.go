// Package voiceover — upload_intent.go (audit P0 #4 commit A/2, Blocco 3.1, July 2026).
//
// UploadIntentUseCase is the application-layer use case that
// orchestrates the upload_intents 5-step lifecycle:
//
//	Step 1: InsertTx     (row created in caller-owned tx as 'pending')
//	Step 2: Drive.Upload (Drive.UploadFile with voiceover payload)
//	Step 3: MarkUploaded (intent row → 'uploaded', drive_file_id stamped)
//	Step 4: ProjectFinalize (local DB finalize — voiceover canonical row)
//	Step 4.5: MarkFinalized (intent row → 'finalized' — bridges Step 4
//	          to Step 5 since MarkCompleted requires status='finalized')
//	Step 5: MarkCompleted (intent row → 'completed')
//
// godlike/07 NO_FAKE_AVAILABILITY contract (semantics per step):
//
//	Step 1 error → return wrapped error; row absent (UNIQUE violation or
//	               SQL error); NO MarkFailed (the row doesn't exist to mark).
//	Step 2 (Drive) error → emit MarkFailed("upload_failed: <cause>"), row at
//	                        'failed'. Reason: Drive upload FAILED so no Drive
//	                        file exists; orphan sweeper (B/2) does NOT need
//	                        to compensate.
//	Step 3 (MarkUploaded) error → if row-absent, surface typed error; else
//	                               emit MarkFailed("mark_uploaded_failed: <cause>").
//	Step 4 (Finalize) error → emit NO MarkFailed, return wrapped error;
//	                           row STAYS AT 'uploaded' (per user-spec contract
//	                           "row resta in `uploaded`"). The orphan sweeper
//	                           (B/2) will detect this state via ListPending
//	                           (filter `WHERE status='pending' OR status='uploaded'`).
//	                           Driving reason: Drive file exists + intent row
//	                           stuck at 'uploaded' = Drive-side orphan that the
//	                           sweeper can compensate (drive.FileDelete + sync
//	                           check, or replay per policy).
//	Step 4.5 (MarkFinalized) error → emit NO MarkFailed, return wrapped
//	                                  error; row STAYS AT 'uploaded' (same
//	                                  orphan-sweeper-visibility contract
//	                                  as Step 4). Reason: Drive file exists
//	                                  (Step 2 OK) + ProjectFinalize-port
//	                                  succeeded (Step 4 OK); only the
//	                                  upload_intents 'uploaded'→'finalized'
//	                                  stamp failed. Orphan sweeper
//	                                  (B/2) retry path will re-run
//	                                  MarkFinalized → MarkCompleted
//	                                  when the transient error resolves.
//	                                  (Audit fix: original implementation
//	                                  emitted MarkFailed here, which would
//	                                  hide the row from the orphan sweeper
//	                                  and prevent Drive-side orphan recovery.)
//	Step 5 (MarkCompleted) error → if row state mismatch (race: already
//	                               completed by parallel worker), treat as
//	                               idempotent success; else emit
//	                               MarkFailed("mark_completed_failed: <cause>")
//	                               per user-spec hard requirement
//	                               ("NON ritornare success se step 5 fallisce
//	                               senza lasciare MarkFailed").
//
// Idempotency NOTE: this commit (A/2) does NOT implement replay
// short-circuit; an idempotent re-run after success will trigger a
// Step-1 InsertTx error (UNIQUE voiceover_id violation), which the use
// case surfaces as a wrapped error. Forward-pointer: commit B/2 or a
// dedicated commit C/2 will add a pre-Step-2 status-check guard
// returning the cached driveFileID for 'completed' rows. Tracked in
// architecture/current.yaml#upload_intents_replay_fix.
package voiceover

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"go.uber.org/zap"
)

// ─── Port surface (Pattern 0, AGENTS.md / godlike/06) ──────────────────────

// UploadIntentsRepository is the application-layer port for the
// upload_intents SQLite row lifecycle. The concrete is
// *scripts.UploadIntentsRepository (in
// internal/platform/sqlite/scripts/upload_intents_repository.go).
// Per AGENTS.md Pattern 0, the consumer declares the interface; the
// production concrete satisfies it by structural conformance (Go's
// implicit-interface rule). Compile-time assertion locks the
// structural conformance in the test surface
// (infrastructure/.../upload_intents_repository_test.go).
type UploadIntentsRepository interface {
	// BeginTx opens a caller-owned transaction for the atomic
	// InsertTx + later inspection sequence (the use case does NOT
	// enforce a Mark* inside the same tx — Mark* methods run
	// against the autocommit db handle for state-machine-pinned
	// visibility per row: a successful MarkUploaded after tx-commit
	// signals to concurrent replicas that this voiceover_id is at
	// 'uploaded').
	BeginTx(ctx context.Context) (*sql.Tx, error)

	// InsertTx persists a new intent row inside the caller-owned
	// tx. Returns (lastInsertID, nil) on success. Returns the raw
	// driver error (UNIQUE violation on voiceover_id surfaces as
	// mattn/go-sqlite3's "UNIQUE constraint failed" error chain)
	// when the row already exists; the caller decides whether to
	// treat this as idempotent-replay or fatal.
	InsertTx(ctx context.Context, tx *sql.Tx, opts *UploadIntentInsertOptions) (int64, error)

	// MarkUploaded transitions the row to 'uploaded' with the
	// given DriveFileID. Returns ErrUploadIntentNotFound when the
	// voiceover_id has no row (godlike/07 typed failure — NOT
	// silent nil). Idempotent on already-uploaded: returns nil
	// (pre-existing state matches the marker's intent — the WHERE
	// filter `status='pending'` returns 0 rows affected in that
	// case, surfacing ErrUploadIntentNotFound).
	MarkUploaded(ctx context.Context, voiceoverID, driveFileID string) error

	// MarkFinalized transitions uploaded → finalized. Returns
	// ErrUploadIntentNotFound when no row matches.
	MarkFinalized(ctx context.Context, voiceoverID string) error

	// MarkCompleted transitions finalized → completed. Returns
	// ErrUploadIntentNotFound when no row matches.
	MarkCompleted(ctx context.Context, voiceoverID string) error

	// MarkFailed transitions pending|uploaded|finalized → failed
	// with explicit reason. Returns ErrUploadIntentNotFound when
	// no row matches. Idempotent on already-failed: returns nil.
	MarkFailed(ctx context.Context, voiceoverID, reason string) error

	// ListPending returns stale 'pending' OR 'uploaded' rows whose
	// updated_at < olderThan. The orphan sweeper (commit B/2) is
	// the only consumer of this method.
	ListPending(ctx context.Context, olderThan time.Time) ([]UploadIntent, error)
}

// UploadIntentInsertOptions is the input bundle for InsertTx. Kept
// as a struct (not variadic) so future fields don't break callers.
type UploadIntentInsertOptions struct {
	VoiceoverID string
	// Attempts is the initial value (typically 0). The attempts
	// column is bumped on every Mark* call (via attempts = attempts + 1
	// in the SQL) so the sweeper can surface repeated-failure rows
	// in operator alerts.
	Attempts int
}

// UploadIntent is the canonical row shape from ListPending. Time
// fields are stored as Unix seconds; the use case and sweeper both
// accept the int64 form.
type UploadIntent struct {
	ID          int64
	VoiceoverID string
	DriveFileID string
	Status      string
	Reason      string
	Attempts    int
	UpdatedUnix int64
}

// ErrUploadIntentNotFound is the canonical application-layer sentinel
// surfaced by every UploadIntentsRepository Mark* method when the
// voiceover_id has no row. The concrete adapter in
// internal/app/lifecycle_adapters.go translates the infra-level
// sentinel (scripts.ErrUploadIntentNotFound) to this application-layer
// sentinel so voiceover never imports the infra package directly.
var ErrUploadIntentNotFound = errors.New("upload_intents_repository: row not found (no intent for voiceover_id)")

// isMarkNotFoundError detects whether an error from any Repo.Mark*
// call is the canonical "row not found" sentinel. The production
// adapter (in internal/app/lifecycle_adapters.go) translates the
// infra-level sentinel to voiceover.ErrUploadIntentNotFound, so
// errors.Is works end-to-end. The substring fallback covers test
// mocks that emit the canonical message text without preserving
// the sentinel pointer chain.
func isMarkNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrUploadIntentNotFound) {
		return true
	}
	return strings.Contains(err.Error(), ErrUploadIntentNotFound.Error())
}

// ProjectFinalizer is the narrow port for the local DB-finalize
// step (Step 4). The voiceover pipeline's finalizeStage already
// does this work via VoiceoverFinalizer (finalizer.go); this port is
// a strictly narrower surface so the use case doesn't import the
// whole package inline.
//
// P2.6 note: the prior `DriveUploaderAdapter` interface (a thin
// adapter over `drive.Admin.UploadFile`) has been retired — Step 2
// now routes through canonical `delivery.Publisher.Publish` per
// DRIVE-CUTOVER-P0-1 closure. The caller-resolved folder is
// preserved via `PublishRequest.ParentFolderID` to keep
// byte-equivalent behaviour vs the prior `UploadFile(ctx, folderID, ...)`
// 3-arg signature.
type ProjectFinalizer interface {
	Finalize(ctx context.Context, voiceoverID, driveFileID string) error
}

// ─── Use case ──────────────────────────────────────────────────────────────

// UploadIntentDeps is the constructor dep bundle (godlike/05
// wiring-error rule: mandatory deps panic on nil).
//
// P2.6: `DriveUploader` retired in favour of `Publisher` carrying
// canonical `delivery.Publisher` per DRIVE-CUTOVER-P0-1 closure.
type UploadIntentDeps struct {
	Repo             UploadIntentsRepository // mandatory
	Publisher        delivery.Publisher      // mandatory (canonical Drive publish; replaces retired DriveUploaderAdapter)
	ProjectFinalizer ProjectFinalizer        // mandatory
	Logger           *zap.Logger             // nil-safe via zap.NewNop()
}

// UploadIntentUseCase is the application-layer orchestrator for the
// 5-step upload-intent lifecycle. Step-4 fail follows the user-spec
// contract literally: row stays at 'uploaded', orphan sweeper picks
// it up. Step-2/3/5 fail emit MarkFailed per godlike/07.
type UploadIntentUseCase struct {
	deps UploadIntentDeps
}

// NewUploadIntentUseCase constructs the canonical use case (panic
// on nil mandatory deps per godlike/05).
func NewUploadIntentUseCase(deps UploadIntentDeps) *UploadIntentUseCase {
	if deps.Repo == nil {
		panic("voiceover.NewUploadIntentUseCase: Repo is required (godlike/05 wiring-error fail-fast)")
	}
	if deps.Publisher == nil {
		panic("voiceover.NewUploadIntentUseCase: Publisher is required (canonical delivery.Publisher; retired DriveUploaderAdapter no longer accepted)")
	}
	if deps.ProjectFinalizer == nil {
		panic("voiceover.NewUploadIntentUseCase: ProjectFinalizer is required")
	}
	if deps.Logger == nil {
		deps.Logger = zap.NewNop()
	}
	return &UploadIntentUseCase{deps: deps}
}

// Execute runs the canonical 5-step lifecycle. See package-doc for
// the full failure semantics + idempotency forward-pointer.
//
// ctx is mandatory; voiceoverID, localPath, folderID are non-empty
// (godlike/05 wiring guards; DO NOT panic on caller-supplied inputs,
// return typed errors).
func (u *UploadIntentUseCase) Execute(ctx context.Context, voiceoverID, localPath, filename, folderID string) (string, error) {
	if voiceoverID == "" {
		return "", fmt.Errorf("UploadIntentUseCase.Execute: empty voiceoverID (godlike/05 wiring-error fail-fast)")
	}
	if localPath == "" {
		return "", fmt.Errorf("UploadIntentUseCase.Execute: empty localPath (finalizer would orphan row to no-payload)")
	}
	if folderID == "" {
		return "", fmt.Errorf("UploadIntentUseCase.Execute: empty folderID (Drive upload requires destination)")
	}

	// ── Step 1: BeginTx + InsertTx + Commit ──
	// InsertTx errors propagate to caller per godlike/07 (no
	// silent-retry, no fake-availability). A UNIQUE-violation
	// surfaces the raw driver error; callers (production routing,
	// orphan sweeper B/2, ops tooling) decide whether the conflict
	// is a recoverable idempotent-replay signal. This commit (A/2)
	// does NOT short-circuit replay — forward-pointer in
	// architecture/current.yaml#upload_intents_replay_fix.
	//
	// CRITICAL (audit fix): the tx is COMMITTED immediately after
	// InsertTx succeeds so the row becomes visible to subsequent
	// Mark* operations. Without Commit, the Mark* methods (which
	// run on the *sql.DB autocommitt handle, NOT on the tx-bound
	// connection per sqlite3 connection isolation) would 0-rows-match
	// every UPDATE and re-emit ErrUploadIntentNotFound. The deferred
	// Rollback is a no-op after Commit per Go's database/sql contract.
	tx, err := u.deps.Repo.BeginTx(ctx)
	if err != nil {
		return "", fmt.Errorf("UploadIntentUseCase.Execute: BeginTx: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit() per database/sql

	if _, err := u.deps.Repo.InsertTx(ctx, tx, &UploadIntentInsertOptions{
		VoiceoverID: voiceoverID,
		Attempts:    0,
	}); err != nil {
		return "", fmt.Errorf("UploadIntentUseCase.Execute: InsertTx: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("UploadIntentUseCase.Execute: tx.Commit (post-InsertTx): %w", err)
	}

	// ── Step 2: Drive.Publish (external side-effect) ──
	// P2.6 migration: routes through canonical `delivery.Publisher.Publish`
	// per architecture/deprecations.yaml#DRIVE-CUTOVER-P0-1 closure.
	// The caller-resolved `folderID` is preserved via
	// `PublishRequest.ParentFolderID` to keep byte-equivalent
	// behaviour vs the prior `drive.Admin.UploadFile(ctx, folderID, ...)`
	// 3-arg signature. Failure semantics unchanged: drive upload
	// failure advances intent row to 'failed' with reason
	// `upload_failed: <cause>`. Sweeper does NOT need to compensate
	// because Drive file does NOT exist on upload failure (no orphan
	// Drive file). The MarkFailed error is logged (warn-level) instead
	// of silently swallowed so operator dashboards can correlate
	// Step-2-fail with missing-MarkFailed (e.g., row absent due to
	// operator delete).
	result, err := u.deps.Publisher.Publish(ctx, delivery.PublishRequest{
		Destination:         delivery.DestinationVoiceover,
		LocalPath:           localPath,
		Filename:            filename,
		DestinationFolderID: folderID,
		ProjectID:           "",
		Style:               "",
	})
	if err != nil {
		if mfErr := u.deps.Repo.MarkFailed(ctx, voiceoverID,
			fmt.Sprintf("upload_failed: %v", err)); mfErr != nil {
			u.deps.Logger.Warn("UploadIntentUseCase.Execute: MarkFailed (post-Step-2-fail) returned error; surfacing publisher error to caller anyway",
				zap.String("voiceover_id", voiceoverID),
				zap.Error(mfErr))
		}
		return "", fmt.Errorf("UploadIntentUseCase.Execute: Publisher.Publish: %w", err)
	}
	driveFileID := result.FileID

	// ── Step 3: MarkUploaded (status 'pending' → 'uploaded', drive_file_id stamped) ──
	if err := u.deps.Repo.MarkUploaded(ctx, voiceoverID, driveFileID); err != nil {
		if isMarkNotFoundError(err) {
			// Row absent — DB-side invariant violation. The orphan
			// sweeper will eventually re-discover (no intent for a
			// real Drive upload). Surface the typed failure; do NOT
			// emit MarkFailed (the row isn't there to mark).
			return "", fmt.Errorf("UploadIntentUseCase.Execute: MarkUploaded: %w (row absent — DB corruption or pre-Step-1 rollback)", err)
		}
		_ = u.deps.Repo.MarkFailed(ctx, voiceoverID,
			fmt.Sprintf("mark_uploaded_failed: %v", err))
		return "", fmt.Errorf("UploadIntentUseCase.Execute: MarkUploaded: %w", err)
	}

	// ── Step 4: ProjectFinalize (local DB finalize — the voiceover
	// pipeline's finalizeStage). USER-SPEC HARD CONTRACT:
	// Drive-had-file + Finalize-failed → row STAYS at 'uploaded' →
	// orphan sweeper (B/2) detects via ListPending → sweeper
	// compensates (Drive.FileDelete or replay per policy). DO NOT
	// emit MarkFailed here: failing this row to 'failed' removes
	// the sweeper's ability to find the orphan (the sweeper
	// filter is `WHERE status='pending' OR status='uploaded'`).
	// MarkFailed on this path would be silent state lost
	// (godlike/07 NO_FAKE_AVAILABILITY violations ≠ here, because
	// the failure is durably logged at the call site + reason
	// column stays empty for orphan-sweeper-visible rows).
	if err := u.deps.ProjectFinalizer.Finalize(ctx, voiceoverID, driveFileID); err != nil {
		return "", fmt.Errorf("UploadIntentUseCase.Execute: ProjectFinalizer.Finalize (intent row left at 'uploaded' for orphan sweeper): %w", err)
	}

	// ── Step 4.5: MarkFinalized (upload_intents row 'uploaded' → 'finalized') ──
	// The canonical upload_intents state machine is a 4-hop chain
	// `pending → uploaded → finalized → completed`. ProjectFinalizer
	// (the port) finalizes the voiceover canonical row — it does
	// NOT transition the upload_intents row. We need an explicit
	// MarkFinalized call here so MarkCompleted's WHERE filter
	// (`status='finalized'`) on Step 5 matches.
	//
	// USER-SPEC CRITICAL CONTRACT (matches Step 4 Finalize-FAIL semantics):
	// On MarkFinalized-FAIL, the row STAYS AT 'uploaded' (NOT 'failed').
	// Reason: orphan sweeper (B/2) filter is `WHERE status='pending' OR
	// status='uploaded'`; emitting MarkFailed would push the row to
	// 'failed', hiding it from the sweeper. The Drive file exists
	// (Step 2 succeeded) so a Drive-side orphan would go unrecovered
	// if the sweeper can't see the row. Sticking at 'uploaded' also
	// lets the sweeper retry MarkFinalized → MarkCompleted when the
	// transient error resolves. Wrapped error is surfaced to the
	// caller for operator visibility (logged at the call site via
	// zap.Error — matches Step 4's logging surface). This is the
	// AUDIT FIX for the original implementation that emitted
	// MarkFailed here (a godlike/07 silent-state-lost violation:
	// spec calls for sweeper-visible state, not operator-fail-state).
	if err := u.deps.Repo.MarkFinalized(ctx, voiceoverID); err != nil {
		if isMarkNotFoundError(err) {
			// Row absent — DB-side invariant violation. Same
			// handling as MarkUploaded's row-absent case above.
			return "", fmt.Errorf("UploadIntentUseCase.Execute: MarkFinalized: %w (row absent — DB corruption or pre-Step-1 rollback)", err)
		}
		return "", fmt.Errorf("UploadIntentUseCase.Execute: MarkFinalized (intent row left at 'uploaded' for orphan sweeper): %w", err)
	}

	// ── Step 5: MarkCompleted (status 'finalized' → 'completed') ──
	// User-spec hard requirement: "NON ritornare success se step 5
	// fallisce senza lasciare MarkFailed". A MarkCompleted failure
	// produces row state 'finalized' (NOT 'completed' yet); the row
	// is functionally complete except for the lifecycle stamp.
	// Emit MarkFailed so operator audit + future sweep cycles see
	// the partial-success state. Race-handled below: a parallel
	// worker may have completed this row already — surface that as
	// idempotent success.
	if err := u.deps.Repo.MarkCompleted(ctx, voiceoverID); err != nil {
		if isMarkNotFoundError(err) {
			// Race: another worker marked 'completed' before us
			// (concurrent retry). The canonical happy path is
			// already recorded; treat as idempotent success.
			u.deps.Logger.Info("UploadIntentUseCase.Execute: MarkCompleted race (already completed by parallel worker); treating as success",
				zap.String("voiceover_id", voiceoverID))
			return driveFileID, nil
		}
		_ = u.deps.Repo.MarkFailed(ctx, voiceoverID,
			fmt.Sprintf("mark_completed_failed: %v", err))
		return "", fmt.Errorf("UploadIntentUseCase.Execute: MarkCompleted: %w", err)
	}

	// All 5 steps succeeded.
	return driveFileID, nil
}
