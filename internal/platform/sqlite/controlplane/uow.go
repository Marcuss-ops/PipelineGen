// Package controlplane contains the concrete SQLite Control Plane adapters.
package controlplane

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	capcontrol "github.com/Marcuss-ops/PipelineGen/internal/capabilities/controlplane"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// UnitOfWork is the SQLite implementation of the canonical mutation boundary.
type UnitOfWork struct {
	db  *sql.DB
	box *outboxevents.Repository
	now func() time.Time
}

// NewUnitOfWork constructs a fail-closed SQLite UoW. The outbox repository
// must use the same database handle as db so its tx-bound enqueue participates
// in the same SQLite transaction.
func NewUnitOfWork(db *sql.DB, box *outboxevents.Repository) (*UnitOfWork, error) {
	if db == nil {
		return nil, errors.New("controlplane uow: nil database")
	}
	if box == nil {
		return nil, errors.New("controlplane uow: nil outbox repository")
	}
	return &UnitOfWork{db: db, box: box, now: time.Now}, nil
}

var _ capcontrol.UnitOfWork = (*UnitOfWork)(nil)

// Run executes a command atomically. A completed command is replayed without
// invoking mutation. If mutation, audit, outbox, or completion fails, the
// entire transaction rolls back, including the idempotency claim.
func (u *UnitOfWork) Run(ctx context.Context, command capcontrol.Command, mutation capcontrol.Mutation) (result capcontrol.Result, err error) {
	if u == nil || u.db == nil || u.box == nil {
		return result, errors.New("controlplane uow: adapter is not configured")
	}
	if err := command.Validate(); err != nil {
		return result, err
	}
	if mutation == nil {
		return result, errors.New("controlplane uow: mutation callback is required")
	}

	tx, err := u.db.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("controlplane uow: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
		if recovered := recover(); recovered != nil {
			panic(recovered)
		}
	}()

	claimed, existing, err := u.claim(ctx, tx, command)
	if err != nil {
		return result, err
	}
	if !claimed {
		result = capcontrol.Result{
			CommandID:      existing.commandID,
			IdempotencyKey: existing.idempotencyKey,
			AlreadyApplied: existing.status == "COMPLETED",
			ResultJSON:     existing.resultJSON,
		}
		switch existing.status {
		case "COMPLETED":
			return result, nil
		case "IN_PROGRESS":
			return capcontrol.Result{}, fmt.Errorf("%w: command_id=%q", capcontrol.ErrCommandInProgress, existing.commandID)
		default:
			return capcontrol.Result{}, fmt.Errorf("controlplane uow: unknown stored status %q", existing.status)
		}
	}

	resultJSON, err := mutation(ctx, tx)
	if err != nil {
		return capcontrol.Result{}, fmt.Errorf("controlplane uow: mutation %q: %w", command.CommandID, err)
	}
	resultJSON = nonEmptyJSON(resultJSON)

	seq, err := u.appendAudit(ctx, tx, command)
	if err != nil {
		return capcontrol.Result{}, err
	}
	outboxResult, err := u.box.Enqueue(ctx, tx,
		command.Outbox.EventType,
		nonEmpty(command.Outbox.AggregateID, command.AggregateID),
		nonEmpty(command.Outbox.AggregateType, command.AggregateType),
		nonEmptyJSON(command.Outbox.PayloadJSON),
		command.Outbox.EventKey,
	)
	if err != nil {
		return capcontrol.Result{}, fmt.Errorf("controlplane uow: enqueue outbox: %w", err)
	}
	if outboxResult == nil {
		return capcontrol.Result{}, errors.New("controlplane uow: outbox returned nil result")
	}

	now := u.now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
		UPDATE canonical_mutations
		SET status='COMPLETED', result_json=?, completed_at=?, error_message=''
		WHERE command_id=? AND status='IN_PROGRESS'`,
		resultJSON, now, command.CommandID); err != nil {
		return capcontrol.Result{}, fmt.Errorf("controlplane uow: complete command %q: %w", command.CommandID, err)
	}

	if err := tx.Commit(); err != nil {
		return capcontrol.Result{}, fmt.Errorf("controlplane uow: commit: %w", err)
	}
	committed = true
	return capcontrol.Result{
		CommandID:      command.CommandID,
		IdempotencyKey: command.IdempotencyKey,
		AlreadyApplied: false,
		ResultJSON:     resultJSON,
		RegistrySeq:    seq,
		OutboxEventID:  outboxResult.EventID,
	}, nil
}

type storedCommand struct {
	commandID      string
	idempotencyKey string
	requestHash    string
	status         string
	resultJSON     string
}

func (u *UnitOfWork) claim(ctx context.Context, tx *sql.Tx, command capcontrol.Command) (bool, storedCommand, error) {
	now := u.now().UTC().Format(time.RFC3339Nano)
	insertResult, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO canonical_mutations
		(command_id, idempotency_key, request_hash, status, result_json, created_at)
		VALUES (?, ?, ?, 'IN_PROGRESS', '{}', ?)`,
		command.CommandID, command.IdempotencyKey, command.RequestHash, now)
	if err != nil {
		return false, storedCommand{}, fmt.Errorf("controlplane uow: claim command %q: %w", command.CommandID, err)
	}
	inserted, err := insertResult.RowsAffected()
	if err != nil {
		return false, storedCommand{}, fmt.Errorf("controlplane uow: inspect claim result %q: %w", command.CommandID, err)
	}

	var stored storedCommand
	err = tx.QueryRowContext(ctx, `
		SELECT command_id, idempotency_key, request_hash, status, result_json
		FROM canonical_mutations
		WHERE command_id=? OR idempotency_key=?
		ORDER BY CASE WHEN command_id=? THEN 0 ELSE 1 END
		LIMIT 1`, command.CommandID, command.IdempotencyKey, command.CommandID).
		Scan(&stored.commandID, &stored.idempotencyKey, &stored.requestHash, &stored.status, &stored.resultJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return false, storedCommand{}, errors.New("controlplane uow: command claim disappeared")
	}
	if err != nil {
		return false, storedCommand{}, fmt.Errorf("controlplane uow: inspect command claim: %w", err)
	}
	if stored.commandID != command.CommandID || stored.idempotencyKey != command.IdempotencyKey ||
		(stored.requestHash != "" && command.RequestHash != "" && stored.requestHash != command.RequestHash) {
		return false, stored, fmt.Errorf("%w: command_id=%q idempotency_key=%q", capcontrol.ErrIdempotencyConflict, command.CommandID, command.IdempotencyKey)
	}
	return inserted == 1, stored, nil
}

func (u *UnitOfWork) appendAudit(ctx context.Context, tx *sql.Tx, command capcontrol.Command) (int64, error) {
	createdAt := command.CreatedAt
	if strings.TrimSpace(createdAt) == "" {
		createdAt = u.now().UTC().Format(time.RFC3339Nano)
	}
	eventID := "mutation:" + command.CommandID
	_, err := tx.ExecContext(ctx, `
		INSERT INTO registry_events
		(event_id, asset_id, event_type, run_id, actor, before_hash, after_hash,
		 payload_json, git_sha, app_version, created_at)
		VALUES (?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?)`,
		eventID, command.AggregateID, command.EventType, "", command.Actor,
		command.BeforeHash, command.AfterHash, nonEmptyJSON(command.PayloadJSON),
		command.GitSHA, command.AppVersion, createdAt)
	if err != nil {
		return 0, fmt.Errorf("controlplane uow: append audit event %q: %w", eventID, err)
	}
	var seq int64
	if err := tx.QueryRowContext(ctx, `SELECT seq FROM registry_events WHERE event_id=?`, eventID).Scan(&seq); err != nil {
		return 0, fmt.Errorf("controlplane uow: read audit sequence %q: %w", eventID, err)
	}
	return seq, nil
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func nonEmptyJSON(value string) string {
	if strings.TrimSpace(value) == "" {
		return "{}"
	}
	return value
}
