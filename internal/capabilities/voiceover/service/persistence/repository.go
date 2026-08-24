package persistence

import (
	"context"
	"database/sql"
)

// Repository is the application-layer port for the voiceovers
// SQLite row lifecycle (P1-2 boundary split, June 2026).
//
// PR-VO-A2 atomic-state requires that every
// DELETE+INSERT+outbox-ENQUEUE chain runs in ONE tx so the OLD
// row is never removed until the NEW row is durably persisted.
// The signature keeps a caller-owned *sql.Tx parameter on every
// write method so atomicity is enforceable at the application
// layer (Service + use case both thread BeginTx→{write,write,
// outbox}→Commit).
//
// PR-VO-B3 dedupe gate runs INSIDE the same tx so the count
// query is consistent with the upcoming INSERT (deletes + inserts
// are visible to each other inside the same SQLite tx).
//
// Nil-safe: processLanguage / finalizeStage guard nil at call
// sites so the optional behaviour degrades to "skip persistence"
// (same pattern as the previous TxOutboxEnqueuer / audioProcessor
// nil-guards).
//
// Production concrete: useCaseRepoAdapter in
// internal/app/adapters_voiceover_use_case.go wrapping
// *sqassets.VoiceoversRepository. Compile-time assertion:
// `var _ persistence.Repository = (*useCaseRepoAdapter)(nil)`.
type Repository interface {
	// BeginTx opens a new tx on the production database. Mirrors
	// *sql.DB.BeginTx(ctx, nil): default isolation (deferred),
	// read-write semantics. The caller is responsible for calling
	// tx.Rollback in a defer (safe after a successful Commit) to
	// release the lock on error.
	BeginTx(ctx context.Context) (*sql.Tx, error)

	// InsertTx persists the new voiceover row in the caller-owned
	// tx. Equivalent to *sqassets.VoiceoversRepository.InsertTx.
	// Mirror schema source-of-truth:
	// internal/platform/sqlite/assets/
	// voiceovers_repository.go::Record. Adding a column here
	// without a SQLite migration will fail at INSERT time, NOT
	// at compile time.
	InsertTx(ctx context.Context, tx *sql.Tx, rec *VoiceoverRecord) error

	// DeleteByIDTx removes the OLD voiceover row in the
	// caller-owned tx. Atomic-swap prerequisite for PR-VO-A2.
	DeleteByIDTx(ctx context.Context, tx *sql.Tx, id string) error

	// PreReadByID reads the OLD voiceover row by id (no tx). Used
	// in processLanguage to capture orphan-candidate paths BEFORE
	// the BeginTx so the post-commit cleanup goroutine has
	// accurate Drive file id + local path surface for the swap.
	//
	// Returns (nil, nil) when no row exists (distinguishes
	// "missing" from "error" via the second return — ProcessLanguage
	// guards errors.Is(rowErr, sql.ErrNoRows) before forcing a
	// swap-and-cleanup).
	PreReadByID(ctx context.Context, id string) (*VoiceoverRecord, error)

	// CountByDriveFileIDTx runs the PR-VO-B3 dedupe gate INSIDE
	// the caller-owned tx. Returns the matched-row id (first hit),
	// the total match count, and any error.
	//
	// Semantics:
	//
	//   - count == 0: no gate fired (`matchedID == ""`).
	//   - count == 1: clean dedupe hit (ambiguity absent).
	//   - count  > 1: ambiguous match — caller logs WARN.
	//
	// Empty driveFileID short-circuits to (matchedID="", count=0,
	// err=nil) — the gate is intentionally a no-op when the stage
	// was called without a Drive upload. Nil-ctx or cancelled-ctx
	// short-circuits to (matchedID="", count=0, err=ctx.Err())
	// so callers can distinguish cancellation from a real match.
	//
	// The SELECT WHERE clause fences the current id out
	// (`id != ?`) so a re-run never shadows its own existence.
	CountByDriveFileIDTx(
		ctx context.Context,
		tx *sql.Tx,
		currentID string,
		driveFileID string,
	) (matchedID string, count int, err error)

	// FindByIdempotencyKeyTx runs the FASE 3 idempotency gate
	// (July 2026) INSIDE the caller-owned tx. The gate fires BEFORE
	// the dedupe gate (Step 1) so a retry of the same job+text+language
	// triple short-circuits the entire 6-step sequence.
	//
	// Returns the matched voiceover row ID when a prior row with the
	// same idempotency_key exists and is still usable (idempotency
	// gate fires). Returns ("", ErrNoRows) when no match exists
	// (first-time run or key collision — the dedupe gate handles
	// the Drive-side check).
	//
	// Empty idempotencyKey short-circuits to ("", sql.ErrNoRows, nil)
	// — the gate is intentionally skipped for pre-FASE-3 callers
	// that don't supply a key (backward-compat).
	FindByIdempotencyKeyTx(
		ctx context.Context,
		tx *sql.Tx,
		idempotencyKey string,
	) (matchedID string, err error)
}

// VoiceoverRecord is the canonical column-set for the voiceovers
// table (P1-2 boundary split, June 2026 — moved from
// voiceover/ports.go so back-compat aliases at the root preserve
// caller compatibility).
//
// Timestamps are RFC3339 strings (matches the JSON-round-trip shape
// emitted through jobs.PayloadMap + the validated parser in
// pkg/timeutil.ParseRFC3339). The concrete adapter
// (useCaseRepoAdapter.toInfraRecord) converts to time.Time under
// the hood so the SQLite layer can index native datetime. This
// indirection means a future time-format migration does NOT
// require touching the application-layer struct.
//
// Schema source-of-truth: internal/platform/sqlite/
// assets/voiceovers_repository.go::Record. The two struct shapes
// are NOT identical: persistence.VoiceoverRecord is the wire shape
// (string timestamps), assets.Record is the SQLite shape
// (time.Time). Keeping the conversion local to the adapter means
// a future schema migration requires only ONE place (the
// converter), not N.
type VoiceoverRecord struct {
	ID        string
	RequestID string
	// TextHash is raw string (NOT the typed TextHash envelope)
	// because the persistence sub-package cannot import the parent
	// voiceover package (Go circular import rule). PR-VO-TEXTHASH-64
	// (August 2026): the DB column now always stores the full 64-char
	// SHA-256 digest regardless of call path.
	TextHash    string
	TextPreview string
	// Language is raw string (NOT the typed Language envelope) because
	// the persistence sub-package cannot import the parent voiceover
	// package (Go circular import rule). The adapter in
	// internal/app/adapters_voiceover_use_case.go converts between
	// the raw string (DB wire shape) and the typed envelope
	// (voiceover-package surface) at the persistence boundary.
	Language        string
	Voice           string
	Filename        string
	LocalPath       string
	CleanedPath     string
	FolderID        string
	FolderPath      string
	DriveFileID     string
	DriveLink       string
	DownloadLink    string
	LegacyFileMD5   string
	DurationSeconds float64
	Status          string
	Error           string
	Strategy        string
	Metadata        string
	CreatedAt       string
	UpdatedAt       string

	// IdempotencyKey is the FASE 3 (July 2026) deterministic retry-safe
	// deduplication key. Stored in the voiceovers.idempotency_key column
	// (migration 132). The UNIQUE INDEX idx_voiceovers_idempotency
	// enforces ONE row per non-empty key; the coarser
	// idx_voiceovers_job_language (migration 133) enforces ONE row per
	// (job_id, language) pair. The Step 0 gate in the finalizer
	// short-circuits the entire 6-step sequence when a match is found.
	IdempotencyKey string

	// JobID is the canonical job identifier that produced this voiceover
	// item. Enables operator audit-trail correlation: "which job run
	// produced this Drive audio file?". Empty JobID is OK (pre-FASE-3
	// rows carry the empty sentinel). The UNIQUE INDEX
	// idx_voiceovers_job_language (migration 133) ensures at most ONE
	// voiceover row per (job_id, language) pair.
	JobID string

	// Fingerprint is the cross-run content/policy cache key. Unlike
	// IdempotencyKey it deliberately excludes the job ID, so identical
	// requests in different jobs can be audited for reuse.
	Fingerprint string
}
