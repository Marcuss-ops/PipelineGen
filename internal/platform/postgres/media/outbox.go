// Package media — outbox.go: the PostgreSQL canonical outbox write surface.
//
// Mirrors internal/platform/sqlite/outboxevents (godlike/06 SSOT: one
// outbox fact, two engine adapters of the same contract). Every code path
// that mutates authoritative media data AND triggers an external
// side-effect routes through Enqueue inside the producer's transaction:
//
//	BEGIN
//	UPDATE media_assets ...
//	INSERT INTO outbox_events (...) ON CONFLICT (event_key) WHERE event_key <> '' DO NOTHING
//	COMMIT
//
// A worker polls ClaimNext and dispatches to the appropriate handler.
// Events have five states:
//   - pending     — eligible for ClaimNext
//   - processing  — claimed by a worker (lease_id non-empty)
//   - completed   — terminal success
//   - dead_letter — terminal failure (terminal error or max_attempts)
//   - superseded  — terminal "skipped" (event obsoleted by a newer
//     aggregate version)
//
// The status column accepts any TEXT (no CHECK constraint on the canonical
// lifecycle set); writes to completed / dead_letter / superseded MUST go
// through the canonical lifecycle mutators to keep lease fencing intact.
package media

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// Outbox event-type and lifecycle constants — canonical values shared with
// the SQLite outbox adapter (single fact family, two engine adapters).
// Do not introduce alternate spellings: event keys are provider-scoped
// across BOTH engines and must remain byte-identical.
const (
	// EventAssetIndexRequested is the canonical asset.index.requested event.
	EventAssetIndexRequested = "asset.index.requested"
	// ReindexEnvelopeV1Schema is the schema_version stamped in the payload.
	ReindexEnvelopeV1Schema = "asset.index.requested.v1"
	// SupersedeStatus is the terminal "skipped" status.
	SupersedeStatus = "superseded"

	// PriorityNormal is the default scheduling priority.
	PriorityNormal = 5
	// PriorityHigh is the script-required index request priority.
	PriorityHigh = 10
)

// EnqueueResult is the typed feedback from Enqueue (SQLite parity:
// outboxevents.EnqueueResult). Inserted=true means the INSERT landed;
// Inserted=false means the conflict arbiter fired and ExistingStatus
// carries the existing row's status.
type EnqueueResult struct {
	EventID               int64
	Inserted              bool
	ExistingStatus        string
	ExistingEventType     string
	ExistingAggregateType string
	ExistingAggregateID   string
}

// Repository wraps SQL access to the outbox_events table over PostgreSQL.
type Repository struct {
	db *sql.DB
}

// NewOutboxRepository creates a Repository backed by db.
func NewOutboxRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// compile-time assertion: *Repository satisfies the narrow surface the
// committer consumes.
var _ outboxRepository = (*Repository)(nil)

// Enqueue inserts one outbox row using the caller-owned transaction.
// The partial unique index ux_outbox_events_event_key (event_key <> ”)
// is the conflict arbiter: non-empty event keys are idempotent-arbitered,
// empty event keys (one-shot inserts) never conflict.
func (r *Repository) Enqueue(ctx context.Context, tx *sql.Tx, eventType, aggregateID, aggregateType, payloadJSON, eventKey string) (*EnqueueResult, error) {
	return r.enqueue(ctx, tx, false, 0, eventType, aggregateID, aggregateType, payloadJSON, eventKey)
}

// EnqueueWithPriority inserts one outbox row with an explicit scheduling
// priority (SQLite parity: outboxevents.EnqueueWithPriority).
func (r *Repository) EnqueueWithPriority(ctx context.Context, tx *sql.Tx, eventType, aggregateID, aggregateType, payloadJSON, eventKey string, priority int) (*EnqueueResult, error) {
	return r.enqueue(ctx, tx, true, priority, eventType, aggregateID, aggregateType, payloadJSON, eventKey)
}

func (r *Repository) enqueue(ctx context.Context, tx *sql.Tx, withPriority bool, priority int, eventType, aggregateID, aggregateType, payloadJSON, eventKey string) (*EnqueueResult, error) {
	now := timeutil.FormatRFC3339(time.Now())
	queryRow := r.db.QueryRowContext
	if tx != nil {
		queryRow = tx.QueryRowContext
	}

	// RETURNING id distinguishes insert from arbiter suppression directly:
	// DO NOTHING returns zero rows when the conflict fired.
	var insertedID int64
	var err error
	if withPriority {
		err = queryRow(ctx, `
			INSERT INTO outbox_events (event_type, aggregate_id, aggregate_type, payload_json, event_key, priority, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (event_key) WHERE event_key <> '' DO NOTHING
			RETURNING id`,
			eventType, aggregateID, aggregateType, payloadJSON, eventKey, priority, now, now).Scan(&insertedID)
	} else {
		err = queryRow(ctx, `
			INSERT INTO outbox_events (event_type, aggregate_id, aggregate_type, payload_json, event_key, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (event_key) WHERE event_key <> '' DO NOTHING
			RETURNING id`,
			eventType, aggregateID, aggregateType, payloadJSON, eventKey, now, now).Scan(&insertedID)
	}
	if err == nil {
		return &EnqueueResult{EventID: insertedID, Inserted: true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("media.outbox.Enqueue(%s, %s): %w", eventType, aggregateID, err)
	}
	// Arbiter suppressed the insert — query the existing row's status so
	// the producer knows whether the event was squelched by a completed,
	// dead_letter, or superseded row. IMPORTANT: use the same handle as
	// the INSERT (tx when provided, db otherwise) so the read observes
	// the conflicting row inside the same snapshot.
	var (
		existingID                                                                    int64
		existingStatus, existingEventType, existingAggregateType, existingAggregateID string
	)
	err = queryRow(ctx, `
		SELECT id, status, event_type, aggregate_type, aggregate_id
		FROM outbox_events WHERE event_key = $1`, eventKey).
		Scan(&existingID, &existingStatus, &existingEventType, &existingAggregateType, &existingAggregateID)
	if err != nil {
		return nil, fmt.Errorf("media.outbox.Enqueue(%s, %s): resolve existing row: %w", eventType, aggregateID, err)
	}
	return &EnqueueResult{
		EventID:               existingID,
		Inserted:              false,
		ExistingStatus:        existingStatus,
		ExistingEventType:     existingEventType,
		ExistingAggregateType: existingAggregateType,
		ExistingAggregateID:   existingAggregateID,
	}, nil
}

// exec returns the right exec-handle based on whether a tx is in scope
// (SQLite mirror: outboxevents.Repository.exec).
func (r *Repository) exec(ctx context.Context, tx *sql.Tx) func(context.Context, string, ...any) (sql.Result, error) {
	if tx != nil {
		return tx.ExecContext
	}
	return r.db.ExecContext
}
