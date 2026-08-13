package controlplane

import (
	"context"
	"errors"
)

type Command struct {
	CommandID, IdempotencyKey, RequestHash string
	AggregateType, AggregateID, Actor      string
	EventType, PayloadJSON                 string
	BeforeHash, AfterHash                  string
	CreatedAt, GitSHA, AppVersion          string
	Outbox                                 OutboxEvent
}

type OutboxEvent struct {
	EventType, AggregateType, AggregateID, PayloadJSON, EventKey string
}

type Transaction interface {
	ExecContext(context.Context, string, ...any) (ExecResult, error)
	QueryRowContext(context.Context, string, ...any) Row
}

// ExecResult mirrors database/sql.Result. sql.Result satisfies it
// structurally, so SQLite adapters can delegate without re-wrapping.
type ExecResult interface {
	LastInsertId() (int64, error)
	RowsAffected() (int64, error)
}

// Row mirrors *sql.Row. *sql.Row satisfies it structurally, so SQLite
// adapters can delegate without re-wrapping.
type Row interface {
	Scan(dest ...any) error
	Err() error
}

type Mutation func(context.Context, Transaction) (string, error)

type Result struct {
	CommandID, IdempotencyKey  string
	AlreadyApplied             bool
	ResultJSON                 string
	RegistrySeq, OutboxEventID int64
}

type UnitOfWork interface {
	// Run owns the transaction lifecycle.
	Run(context.Context, Command, Mutation) (Result, error)
	// RunInTransaction applies the same protocol in a caller-owned transaction.
	RunInTransaction(context.Context, Transaction, Command, Mutation) (Result, error)
}

var (
	ErrCommandIDRequired      = errors.New("controlplane: command_id is required")
	ErrIdempotencyKeyRequired = errors.New("controlplane: idempotency_key is required")
	ErrAggregateRequired      = errors.New("controlplane: aggregate identity is required")
	ErrRequestHashRequired    = errors.New("controlplane: request_hash is required")
	ErrEventTypeRequired      = errors.New("controlplane: audit event type is required")
	ErrOutboxRequired         = errors.New("controlplane: transactional outbox event is required")
	ErrIdempotencyConflict    = errors.New("controlplane: idempotency key conflicts with a different command or request")
	ErrCommandInProgress      = errors.New("controlplane: command is already in progress")
	ErrOutboxTerminalConflict = errors.New("controlplane: outbox event already has terminal status")
)

func (c Command) Validate() error {
	if c.CommandID == "" {
		return ErrCommandIDRequired
	}
	if c.IdempotencyKey == "" {
		return ErrIdempotencyKeyRequired
	}
	if c.AggregateType == "" || c.AggregateID == "" {
		return ErrAggregateRequired
	}
	if c.RequestHash == "" {
		return ErrRequestHashRequired
	}
	if c.EventType == "" {
		return ErrEventTypeRequired
	}
	if c.Outbox.EventType == "" || c.Outbox.EventKey == "" {
		return ErrOutboxRequired
	}
	return nil
}
