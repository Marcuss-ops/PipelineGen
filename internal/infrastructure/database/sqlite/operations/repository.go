// Package operations — repository.go is the canonical concrete
// adapter for the `operations` SQLite table (migration 145).
//
// godlike/06 SSOT: this file is the SOLE canonical owner of the
// SQL projection + scan helper for the operations table. Domain
// types live in `internal/domain/operations`; the application
// service in `internal/application/operations` consumes only
// the `Repository` port defined here.
//
// godlike/07 minimum-blast-radius: every public method takes an
// optional `*sql.Tx` (nil = use the repository's *sql.DB). This
// keeps the FASE 2 atomic-TX shape (operation + job + outbox in
// ONE transaction) transparent at the call site — the caller
// passes a `*sql.Tx` for the atomic path, `nil` for the
// single-statement path.
//
// Thread safety: the repository holds a `*sql.DB` which is
// safe for concurrent use per database/sql docs. SQLite single-
// writer semantics are enforced by the application layer
// (`BEGIN IMMEDIATE` in GenerationSubmissionService.Submit).
package operations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/operations"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// Repository is the canonical port for the operations table.
// Implementations must be safe for concurrent use.
//
// godlike/06 SSOT: this interface is the typed-port surface that
// `internal/application/operations` consumes. The concrete SQLite
// adapter (`*SQLiteRepository`) is the only production
// implementation; tests may use in-memory fakes.
type Repository interface {
	// Insert atomically writes a new operation row. Returns
	// `operations.ErrInvalidOperationScope` or
	// `operations.ErrInvalidOperationState` for out-of-set values
	// (godlike/07 fail-closed). Returns the underlying SQLite
	// error for any other failure (e.g. disk full, schema drift).
	//
	// When `tx != nil`, the insert participates in the caller's
	// transaction; when `tx == nil`, the insert runs in its own
	// implicit transaction (database/sql default).
	Insert(ctx context.Context, op *operations.Operation, tx *sql.Tx) error

	// GetByID returns the operation with the given primary-key
	// operation_id, or (nil, operations.ErrOperationNotFound) when
	// no row matches.
	GetByID(ctx context.Context, id string, tx *sql.Tx) (*operations.Operation, error)

	// GetLatestForKey returns the most recent operation for a
	// (scope, idempotency_key) pair, or (nil, nil) when no row
	// matches. The lookup uses the idx_operations_idem_lookup
	// composite index (migration 145). Used by the idempotency-
	// hit path AND the force_refresh-supersedes path.
	GetLatestForKey(ctx context.Context, scope operations.Scope, idempotencyKey string, tx *sql.Tx) (*operations.Operation, error)

	// UpdateState transitions the operation to newState. The
	// updated_at column is bumped to the call time. Returns
	// `operations.ErrOperationNotFound` when no row matches the
	// given id. Used by the force_refresh path to flip the
	// prior operation's state to SUPERSEDED in the same atomic
	// TX as the new operation's INSERT.
	UpdateState(ctx context.Context, id string, newState operations.State, tx *sql.Tx) error
}

// SQLiteRepository is the canonical concrete adapter for the
// operations table. Holds a *sql.DB; thread-safe.
type SQLiteRepository struct {
	db *sql.DB
}

// NewSQLiteRepository constructs the canonical repository. The
// caller owns the *sql.DB lifecycle (the application composition
// root in internal/app is the sole owner; this constructor is
// nil-safe on the *sql.DB and panics on nil to fail-fast at
// composition time per godlike/07).
func NewSQLiteRepository(db *sql.DB) *SQLiteRepository {
	if db == nil {
		panic("operations.NewSQLiteRepository: db is nil (composition bug)")
	}
	return &SQLiteRepository{db: db}
}

// Compile-time guard: SQLiteRepository implements Repository.
var _ Repository = (*SQLiteRepository)(nil)

// operationColumns is the canonical column projection for the
// operations table. Kept in one place so adding a tracked column
// is a one-line change. MUST match the column order in scanOperation
// below.
const operationColumns = `operation_id, scope, idempotency_key, request_hash, job_id, state,
	created_at, updated_at, supersedes_operation_id`

// operationScanner is the minimum *sql.Row / *sql.Rows surface
// for scanOperation. Defined inline so single-row and multi-row
// reads share the same scan code.
type operationScanner interface {
	Scan(dest ...any) error
}

// scanOperation reads one operation row into op. Used by GetByID
// and GetLatestForKey.
//
// godlike/06 SSOT: timeutil.ParseRFC3339 returns (time.Time) only —
// per the canonical pkg/timeutil surface (sig locked in
// pkg/timeutil/timeutil.go:20).
//
// Push 2.1 (MEDIUM code-review fix): parse failures (e.g. DB
// corruption, manual row tampering) now surface as the typed
// ErrCorruptedOperationRow sentinel, NOT as a silent zero
// time.Time. The caller (GetByID / GetLatestForKey) propagates
// the sentinel so the HTTP layer can surface 500 + an admin
// alert (godlike/07 fail-closed). The pre-fix version returned
// a zero-value CreatedAt/UpdatedAt that was indistinguishable
// from a legitimate value, masking DB corruption.
func scanOperation(s operationScanner, op *operations.Operation) error {
	var scopeStr, stateStr, createdAt, updatedAt string
	if err := s.Scan(
		&op.OperationID, &scopeStr, &op.IdempotencyKey, &op.RequestHash,
		&op.JobID, &stateStr,
		&createdAt, &updatedAt, &op.SupersedesOperationID,
	); err != nil {
		return err
	}
	op.Scope = operations.Scope(scopeStr)
	op.State = operations.State(stateStr)

	created := timeutil.ParseRFC3339(createdAt)
	if created.IsZero() {
		return fmt.Errorf("%w: created_at=%q (operation_id=%q)",
			operations.ErrCorruptedOperationRow, createdAt, op.OperationID)
	}
	updated := timeutil.ParseRFC3339(updatedAt)
	if updated.IsZero() {
		return fmt.Errorf("%w: updated_at=%q (operation_id=%q)",
			operations.ErrCorruptedOperationRow, updatedAt, op.OperationID)
	}
	op.CreatedAt = created
	op.UpdatedAt = updated
	return nil
}

// validateForWrite enforces the canonical Scope + State + field
// invariants BEFORE any SQL runs. godlike/07 fail-closed: a
// bogus value is rejected at the input boundary, not silently
// persisted to a column with no DB-level CHECK constraint.
//
// Push 2.1 (HIGH severity code-review fix): CreatedAt and UpdatedAt
// are REQUIRED to be non-zero. The pre-fix Insert auto-filled zero
// timestamps with time.Now(), which silently decoupled the
// operation's created_at from the underlying job's created_at
// (audit invariant break). The canonical time source is the
// caller (the service layer in push 2.2 stamps the operation
// with the same time it stamps the job, guaranteeing the two
// rows have the same created_at to the millisecond).
func validateForWrite(op *operations.Operation) error {
	if op == nil {
		return fmt.Errorf("operations: nil operation")
	}
	if op.OperationID == "" {
		return fmt.Errorf("operations: empty operation_id")
	}
	if !op.Scope.IsValid() {
		return fmt.Errorf("%w: %q", operations.ErrInvalidOperationScope, op.Scope)
	}
	if !op.State.IsValid() {
		return fmt.Errorf("%w: %q", operations.ErrInvalidOperationState, op.State)
	}
	if !operations.IsValidIdempotencyKey(op.IdempotencyKey) {
		return operations.ErrIdempotencyKeyInvalid
	}
	if !operations.IsValidRequestHash(op.RequestHash) {
		return operations.ErrRequestHashInvalid
	}
	if op.JobID == "" {
		return fmt.Errorf("operations: empty job_id (FASE 2 contract: every operation has a job)")
	}
	if op.SupersedesOperationID != "" && op.SupersedesOperationID == op.OperationID {
		return fmt.Errorf("%w: operation_id=%q",
			operations.ErrSelfSupersedeReference, op.OperationID)
	}
	if op.CreatedAt.IsZero() {
		return fmt.Errorf("operations: zero CreatedAt (caller MUST stamp; pre-fix auto-fill was a footgun)")
	}
	if op.UpdatedAt.IsZero() {
		return fmt.Errorf("operations: zero UpdatedAt (caller MUST stamp; pre-fix auto-fill was a footgun)")
	}
	return nil
}

// Insert atomically writes a new operation row. See Repository.Insert.
//
// Push 2.1 (HIGH severity code-review fix): the pre-fix version
// auto-filled zero CreatedAt/UpdatedAt with time.Now(). That was
// a footgun — it silently decoupled the operation's created_at
// from the underlying job's created_at, breaking the audit
// invariant that "the operation and its job were committed in
// the same atomic TX at the same time". The canonical fix is
// to REQUIRE the caller to stamp both fields explicitly
// (validateForWrite rejects zero-value timestamps) so the
// service layer is forced to think about the time source and
// stamp both rows with the same value inside the atomic TX.
func (r *SQLiteRepository) Insert(ctx context.Context, op *operations.Operation, tx *sql.Tx) error {
	if err := validateForWrite(op); err != nil {
		return err
	}

	query := `INSERT INTO operations (` + operationColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

	args := []any{
		op.OperationID, string(op.Scope), op.IdempotencyKey, op.RequestHash,
		op.JobID, string(op.State),
		timeutil.FormatRFC3339(op.CreatedAt), timeutil.FormatRFC3339(op.UpdatedAt),
		op.SupersedesOperationID,
	}

	var err error
	if tx != nil {
		_, err = tx.ExecContext(ctx, query, args...)
	} else {
		_, err = r.db.ExecContext(ctx, query, args...)
	}
	if err != nil {
		return fmt.Errorf("operations.Insert: %w", err)
	}
	return nil
}

// GetByID returns the operation with the given primary-key
// operation_id, or (nil, operations.ErrOperationNotFound) when
// no row matches.
func (r *SQLiteRepository) GetByID(ctx context.Context, id string, tx *sql.Tx) (*operations.Operation, error) {
	if id == "" {
		return nil, fmt.Errorf("operations.GetByID: empty operation_id")
	}
	query := `SELECT ` + operationColumns + ` FROM operations WHERE operation_id = ?`
	var row *sql.Row
	if tx != nil {
		row = tx.QueryRowContext(ctx, query, id)
	} else {
		row = r.db.QueryRowContext(ctx, query, id)
	}
	op := &operations.Operation{}
	if err := scanOperation(row, op); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, operations.WrapOperationNotFound(id)
		}
		return nil, fmt.Errorf("operations.GetByID: %w", err)
	}
	return op, nil
}

// GetLatestForKey returns the most recent operation for a
// (scope, idempotency_key) pair, or (nil, nil) when no row
// matches. The lookup uses the idx_operations_idem_lookup
// composite index (migration 145).
func (r *SQLiteRepository) GetLatestForKey(ctx context.Context, scope operations.Scope, idempotencyKey string, tx *sql.Tx) (*operations.Operation, error) {
	if !scope.IsValid() {
		return nil, fmt.Errorf("%w: %q", operations.ErrInvalidOperationScope, scope)
	}
	if !operations.IsValidIdempotencyKey(idempotencyKey) {
		return nil, operations.ErrIdempotencyKeyInvalid
	}
	query := `SELECT ` + operationColumns + ` FROM operations
		WHERE scope = ? AND idempotency_key = ?
		ORDER BY created_at DESC, operation_id DESC
		LIMIT 1`
	var row *sql.Row
	if tx != nil {
		row = tx.QueryRowContext(ctx, query, string(scope), idempotencyKey)
	} else {
		row = r.db.QueryRowContext(ctx, query, string(scope), idempotencyKey)
	}
	op := &operations.Operation{}
	if err := scanOperation(row, op); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("operations.GetLatestForKey: %w", err)
	}
	return op, nil
}

// UpdateState transitions the operation to newState and bumps
// updated_at to time.Now(). Returns
// `operations.ErrOperationNotFound` when no row matches.
//
// The newState value is validated against the canonical State
// enum (godlike/07 fail-closed) BEFORE any SQL runs.
//
// Used by the force_refresh path: when a new operation is
// created with `supersedes_operation_id` set, the prior
// operation's state is flipped to SUPERSEDED in the SAME
// atomic TX as the new operation's INSERT.
func (r *SQLiteRepository) UpdateState(ctx context.Context, id string, newState operations.State, tx *sql.Tx) error {
	if id == "" {
		return fmt.Errorf("operations.UpdateState: empty operation_id")
	}
	if !newState.IsValid() {
		return fmt.Errorf("%w: %q", operations.ErrInvalidOperationState, newState)
	}
	now := time.Now()
	query := `UPDATE operations SET state = ?, updated_at = ? WHERE operation_id = ?`
	var res sql.Result
	var err error
	if tx != nil {
		res, err = tx.ExecContext(ctx, query, string(newState), timeutil.FormatRFC3339(now), id)
	} else {
		res, err = r.db.ExecContext(ctx, query, string(newState), timeutil.FormatRFC3339(now), id)
	}
	if err != nil {
		return fmt.Errorf("operations.UpdateState: %w", err)
	}
	rows, rowsErr := res.RowsAffected()
	if rowsErr != nil {
		return fmt.Errorf("operations.UpdateState: RowsAffected: %w", rowsErr)
	}
	if rows == 0 {
		return operations.WrapOperationNotFound(id)
	}
	return nil
}
