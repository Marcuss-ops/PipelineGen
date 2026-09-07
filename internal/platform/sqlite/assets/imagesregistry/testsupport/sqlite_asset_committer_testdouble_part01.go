// Package testsupport — TEST-ONLY SQLite AssetCommitter.
//
// POSTGRES-MEDIA-CUTOVER demolition note: the production SQLite media
// writer family was REMOVED (the canonical media writer is
// PostgresMediaCommitter over PostgreSQL + pgvector). Legacy engine-level
// test suites (finalizer, catalogsync, artlist integration, jobs, youtube
// adapters) still exercise the AssetCommitter CONTRACT against a hermetic
// SQLite engine. This package provides that test double, clearly marked
// test-only: it is NEVER imported by production code.
package testsupport

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	capcontrol "github.com/Marcuss-ops/PipelineGen/internal/capabilities/controlplane"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	sqlitecontrol "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/controlplane"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	"go.uber.org/zap"
	"strings"
	"time"
)

// SQLiteAssetCommitter is the canonical adapter for
// persistence.AssetCommitter.
type SQLiteAssetCommitter struct {
	db  *sql.DB
	box *outboxevents.Repository
	log *zap.Logger
	uow capcontrol.UnitOfWork
}

// NewSQLiteAssetCommitter constructs the adapter. Both db and box are
// required; a nil value panics at construction time so wiring gaps
// surface at boot rather than at first commit.
func NewSQLiteAssetCommitter(db *sql.DB, box *outboxevents.Repository, log *zap.Logger) *SQLiteAssetCommitter {
	if db == nil {
		panic("assets.NewSQLiteAssetCommitter: db is required")
	}
	if box == nil {
		panic("assets.NewSQLiteAssetCommitter: outboxevents.Repository is required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &SQLiteAssetCommitter{db: db, box: box, log: log, uow: discoverUnitOfWork(db, box)}
}

func discoverUnitOfWork(db *sql.DB, box *outboxevents.Repository) capcontrol.UnitOfWork {
	var present int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='canonical_mutations'`).Scan(&present); err != nil {
		if strings.Contains(err.Error(), "database is closed") {
			return nil
		}
		panic(fmt.Sprintf("assets.NewSQLiteAssetCommitter: inspect canonical UoW schema: %v", err))
	}
	if present == 1 {
		for _, table := range []string{"registry_events", "outbox_events"} {
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil || count != 1 {
				panic(fmt.Sprintf("assets.NewSQLiteAssetCommitter: required canonical table %q is missing", table))
			}
		}
		for _, column := range []string{"registry_seq", "outbox_event_id"} {
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('canonical_mutations') WHERE name=?`, column).Scan(&count); err != nil || count != 1 {
				panic(fmt.Sprintf("assets.NewSQLiteAssetCommitter: canonical_mutations.%s is missing", column))
			}
		}
		uow, err := sqlitecontrol.NewUnitOfWork(db, box)
		if err != nil {
			panic(fmt.Sprintf("assets.NewSQLiteAssetCommitter: initialize canonical UoW: %v", err))
		}
		return uow
	}
	var ledger int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'`).Scan(&ledger); err == nil && ledger == 1 {
		panic("assets.NewSQLiteAssetCommitter: canonical_mutations missing from migrated database")
	}
	return nil
}

// Compile-time assertion.
var _ persistence.AssetCommitter = (*SQLiteAssetCommitter)(nil)

// CommitAsset is the canonical user-facing entry point. It opens a fresh
// SQLite transaction, writes the canonical asset, locations, metadata and
// durable indexing request, then commits atomically.
func (c *SQLiteAssetCommitter) CommitAsset(ctx context.Context, req persistence.AssetCommitRequest) (persistence.CommittedAsset, error) {
	return c.CommitAndIndex(ctx, persistence.CommitRequest(req))
}

// CommitAndIndex opens a new transaction, writes the asset, and commits.
// This is the standalone-producer entry point.
//
// Canonical-UoW routing: when the database carries the canonical_mutations
// protocol AND the request emits an index event, the commit runs through the
// UoW so the asset write and the outbox event share one idempotency claim.
// Requests without an index event (folder upserts, legacy store saves) take
// the raw path: the UoW protocol exists to make the asset+event pair atomic
// and replay-safe, and the media_assets UPSERT is idempotent on its own.
func (c *SQLiteAssetCommitter) CommitAndIndex(ctx context.Context, req persistence.CommitRequest) (persistence.CommitResult, error) {
	if c.uow != nil && req.EmitIndexEvent {
		return c.commitWithUnitOfWork(ctx, req)
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return persistence.CommitResult{}, fmt.Errorf("asset committer: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	res, err := c.CommitTx(ctx, tx, req)
	if err != nil {
		return persistence.CommitResult{}, err
	}

	if err := tx.Commit(); err != nil {
		return persistence.CommitResult{}, fmt.Errorf("asset committer: commit: %w", err)
	}
	committed = true

	// Post-commit terminal-conflict checks cover both the canonical index
	// event and every additional durable intent. The asset row is already
	// owned by SQLite; a terminal intent is surfaced so callers can trigger
	// explicit recovery instead of reporting an unqualified success.
	if !res.OutboxInserted && res.OutboxEventKey != "" {
		status, err := c.queryOutboxStatus(ctx, res.OutboxEventKey)
		if err == nil && isTerminalOutboxStatus(status) {
			return res, fmt.Errorf("%w: event_key=%q status=%q", persistence.ErrAssetCommitOutboxTerminal, res.OutboxEventKey, status)
		}
	}
	for _, additional := range res.AdditionalOutbox {
		if !additional.Inserted && isTerminalOutboxStatus(additional.ExistingStatus) {
			return res, fmt.Errorf("%w: event_key=%q status=%q", persistence.ErrAssetCommitOutboxTerminal, additional.EventKey, additional.ExistingStatus)
		}
	}

	return res, nil
}

// CommitTx writes the asset, locations, metadata and optional indexing
// request inside the caller-owned transaction. On migrated databases it
// applies the canonical UoW protocol without taking ownership of the tx,
// but only when the request emits an index event (see CommitAndIndex:
// event-less commits have no event idempotency to uphold, so the raw
// writer runs directly).
func (c *SQLiteAssetCommitter) CommitTx(ctx context.Context, tx persistence.Transaction, req persistence.CommitRequest) (persistence.CommitResult, error) {
	if c.uow != nil && req.EmitIndexEvent {
		return c.commitTxWithUnitOfWork(ctx, tx, req)
	}
	return c.CommitTxRaw(ctx, tx, req)
}

func (c *SQLiteAssetCommitter) commitTxWithUnitOfWork(ctx context.Context, tx persistence.Transaction, req persistence.CommitRequest) (persistence.CommitResult, error) {
	if err := normalizeIndexTaxonomy(&req); err != nil {
		return persistence.CommitResult{}, err
	}
	if err := req.Validate(); err != nil {
		return persistence.CommitResult{}, err
	}
	if !req.EmitIndexEvent {
		return persistence.CommitResult{}, fmt.Errorf("asset committer: canonical UoW requires EmitIndexEvent=true")
	}
	command, err := buildAssetMutationCommand(req)
	if err != nil {
		return persistence.CommitResult{}, err
	}
	sqlTx, ok := tx.(*sql.Tx)
	if !ok || sqlTx == nil {
		return persistence.CommitResult{}, fmt.Errorf("asset committer: expected *sql.Tx, got %T", tx)
	}
	result, err := c.uow.RunInTransaction(ctx, sqlitecontrol.WrapTx(sqlTx), command, func(ctx context.Context, uowTx capcontrol.Transaction) (string, error) {
		uowSQLTx, ok := sqlitecontrol.UnwrapSQLTx(uowTx)
		if !ok || uowSQLTx == nil {
			return "", fmt.Errorf("asset committer: uow transaction is not a sqlite transaction")
		}
		// The UoW command owns the durable index-request emission for this
		// claim (buildAssetMutationCommand). Emit again inside the mutation
		// and the same commit inserts TWO asset.index.requested rows.
		committed, mutationErr := c.commitTxRawNoEvent(ctx, uowSQLTx, req)
		if mutationErr != nil {
			return "", mutationErr
		}
		payload, marshalErr := json.Marshal(committed)
		if marshalErr != nil {
			return "", marshalErr
		}
		return string(payload), nil
	})
	if err != nil {
		return persistence.CommitResult{}, err
	}
	var committed persistence.CommitResult
	if err := json.Unmarshal([]byte(result.ResultJSON), &committed); err != nil {
		return persistence.CommitResult{}, fmt.Errorf("asset committer: decode UoW result: %w", err)
	}
	return committed, nil
}

func (c *SQLiteAssetCommitter) commitWithUnitOfWork(ctx context.Context, req persistence.CommitRequest) (persistence.CommitResult, error) {
	if err := normalizeIndexTaxonomy(&req); err != nil {
		return persistence.CommitResult{}, err
	}
	if err := req.Validate(); err != nil {
		return persistence.CommitResult{}, err
	}
	if !req.EmitIndexEvent {
		return persistence.CommitResult{}, fmt.Errorf("asset committer: canonical UoW requires EmitIndexEvent=true")
	}
	command, err := buildAssetMutationCommand(req)
	if err != nil {
		return persistence.CommitResult{}, err
	}
	result, err := c.uow.Run(ctx, command, func(ctx context.Context, tx capcontrol.Transaction) (string, error) {
		sqlTx, ok := sqlitecontrol.UnwrapSQLTx(tx)
		if !ok || sqlTx == nil {
			return "", fmt.Errorf("asset committer: uow transaction is not a sqlite transaction")
		}
		// Same as commitTxWithUnitOfWork: the UoW claim owns the event.
		committed, mutationErr := c.commitTxRawNoEvent(ctx, sqlTx, req)
		if mutationErr != nil {
			return "", mutationErr
		}
		payload, marshalErr := json.Marshal(committed)
		if marshalErr != nil {
			return "", marshalErr
		}
		return string(payload), nil
	})
	if err != nil {
		return persistence.CommitResult{}, err
	}
	var committed persistence.CommitResult
	if err := json.Unmarshal([]byte(result.ResultJSON), &committed); err != nil {
		return persistence.CommitResult{}, fmt.Errorf("asset committer: decode UoW result: %w", err)
	}
	return committed, nil
}

// commitTxRawNoEvent runs CommitTxRaw with the durable index-request
// emission suppressed. The canonical UoW owns the outbox write for the claim
// (see buildAssetMutationCommand); emitting again inside the mutation would
// insert a second asset.index.requested row for the same commit.
func (c *SQLiteAssetCommitter) commitTxRawNoEvent(ctx context.Context, sqlTx *sql.Tx, req persistence.CommitRequest) (persistence.CommitResult, error) {
	noEvent := req
	noEvent.EmitIndexEvent = false
	return c.CommitTxRaw(ctx, sqlTx, noEvent)
}

func buildAssetMutationCommand(req persistence.CommitRequest) (capcontrol.Command, error) {
	fingerprint, err := commitRequestFingerprint(req)
	if err != nil {
		return capcontrol.Command{}, fmt.Errorf("asset committer: build mutation fingerprint: %w", err)
	}
	outboxEvent, err := buildAssetMutationOutboxEvent(req)
	if err != nil {
		return capcontrol.Command{}, err
	}
	commandID := fmt.Sprintf("asset-commit:%s:%s", req.AssetID, fingerprint)
	return capcontrol.Command{
		CommandID: commandID, IdempotencyKey: commandID, RequestHash: fingerprint,
		AggregateType: "media_asset", AggregateID: req.AssetID, Actor: "asset-committer",
		EventType:   "MEDIA_ASSET_MUTATED",
		PayloadJSON: fmt.Sprintf(`{"asset_id":%q,"request_hash":%q}`, req.AssetID, fingerprint),
		Outbox:      outboxEvent,
	}, nil
}

func commitRequestFingerprint(req persistence.CommitRequest) (string, error) {
	req.RequestedAt = time.Time{}
	payload, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	sum := digest.SHA256Bytes(payload)
	return sum, nil
}
