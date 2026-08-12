package controlplane

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	capcontrol "github.com/Marcuss-ops/PipelineGen/internal/capabilities/controlplane"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	_ "github.com/mattn/go-sqlite3"
)

const uowSchema = `
CREATE TABLE canonical_mutations (
 command_id TEXT PRIMARY KEY,
 idempotency_key TEXT NOT NULL UNIQUE,
 request_hash TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL,
 result_json TEXT NOT NULL DEFAULT '{}',
 created_at TEXT NOT NULL,
 completed_at TEXT,
 error_message TEXT NOT NULL DEFAULT ''
);
CREATE TABLE registry_events (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 event_id TEXT NOT NULL UNIQUE,
 asset_id TEXT,
 event_type TEXT NOT NULL,
 run_id TEXT,
 actor TEXT NOT NULL DEFAULT '',
 before_hash TEXT NOT NULL DEFAULT '',
 after_hash TEXT NOT NULL DEFAULT '',
 payload_json TEXT NOT NULL DEFAULT '{}',
 git_sha TEXT NOT NULL DEFAULT '',
 app_version TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL
);
CREATE TABLE outbox_events (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 event_type TEXT NOT NULL,
 aggregate_id TEXT NOT NULL DEFAULT '',
 aggregate_type TEXT NOT NULL DEFAULT '',
 payload_json TEXT NOT NULL DEFAULT '',
 event_key TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL DEFAULT 'pending',
 attempt_count INTEGER NOT NULL DEFAULT 0,
 max_attempts INTEGER NOT NULL DEFAULT 10,
 last_error TEXT NOT NULL DEFAULT '',
 next_attempt_at TEXT,
 worker_id TEXT NOT NULL DEFAULT '',
 lease_id TEXT NOT NULL DEFAULT '',
 lease_expiry TEXT,
 completed_at TEXT,
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX ux_outbox_events_event_key ON outbox_events(event_key);
CREATE TABLE mutation_targets (id TEXT PRIMARY KEY, value TEXT NOT NULL);
`

func newUOWForTest(t *testing.T) (*UnitOfWork, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:uow_"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(uowSchema); err != nil {
		t.Fatal(err)
	}
	uow, err := NewUnitOfWork(db, outboxevents.NewRepository(db))
	if err != nil {
		t.Fatal(err)
	}
	return uow, db
}

func testCommand() capcontrol.Command {
	return capcontrol.Command{
		CommandID: "cmd-1", IdempotencyKey: "idem-1", RequestHash: "hash-1",
		AggregateType: "mutation_target", AggregateID: "asset-1", Actor: "test",
		EventType: "ASSET_UPDATED", PayloadJSON: `{"value":"v1"}`,
		Outbox: capcontrol.OutboxEvent{
			EventType: "asset.updated", AggregateType: "mutation_target",
			AggregateID: "asset-1", PayloadJSON: `{"value":"v1"}`, EventKey: "outbox:cmd-1",
		},
	}
}

func TestUnitOfWorkCommitsMutationAuditOutboxAndCommand(t *testing.T) {
	uow, db := newUOWForTest(t)
	result, err := uow.Run(context.Background(), testCommand(), func(ctx context.Context, tx capcontrol.Transaction) (string, error) {
		_, err := tx.ExecContext(ctx, `INSERT INTO mutation_targets(id, value) VALUES (?, ?)`, "asset-1", "v1")
		return `{"ok":true}`, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AlreadyApplied || result.RegistrySeq != 1 || result.OutboxEventID == 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	assertCount(t, db, "SELECT COUNT(*) FROM mutation_targets", 1)
	assertCount(t, db, "SELECT COUNT(*) FROM registry_events", 1)
	assertCount(t, db, "SELECT COUNT(*) FROM outbox_events", 1)
	assertCount(t, db, "SELECT COUNT(*) FROM canonical_mutations WHERE status='COMPLETED'", 1)
}

func TestUnitOfWorkReplayDoesNotRunMutationAgain(t *testing.T) {
	uow, db := newUOWForTest(t)
	calls := 0
	mutation := func(ctx context.Context, tx capcontrol.Transaction) (string, error) {
		calls++
		_, err := tx.ExecContext(ctx, `INSERT INTO mutation_targets(id, value) VALUES (?, ?)`, "asset-1", "v1")
		return `{"ok":true}`, err
	}
	if _, err := uow.Run(context.Background(), testCommand(), mutation); err != nil {
		t.Fatal(err)
	}
	result, err := uow.Run(context.Background(), testCommand(), func(context.Context, capcontrol.Transaction) (string, error) {
		calls++
		return `{"unexpected":true}`, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.AlreadyApplied || result.ResultJSON != `{"ok":true}` || calls != 1 {
		t.Fatalf("replay was not idempotent: result=%+v calls=%d", result, calls)
	}
	assertCount(t, db, "SELECT COUNT(*) FROM registry_events", 1)
	assertCount(t, db, "SELECT COUNT(*) FROM outbox_events", 1)
}

func TestUnitOfWorkRollsBackMutationAndAuditWhenMutationFails(t *testing.T) {
	uow, db := newUOWForTest(t)
	wantErr := errors.New("boom")
	_, err := uow.Run(context.Background(), testCommand(), func(ctx context.Context, tx capcontrol.Transaction) (string, error) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO mutation_targets(id, value) VALUES (?, ?)`, "asset-1", "v1"); err != nil {
			return "", err
		}
		return "", wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("mutation error = %v, want %v", err, wantErr)
	}
	assertCount(t, db, "SELECT COUNT(*) FROM mutation_targets", 0)
	assertCount(t, db, "SELECT COUNT(*) FROM registry_events", 0)
	assertCount(t, db, "SELECT COUNT(*) FROM outbox_events", 0)
	assertCount(t, db, "SELECT COUNT(*) FROM canonical_mutations", 0)
}

func TestUnitOfWorkRequiresRequestHash(t *testing.T) {
	uow, _ := newUOWForTest(t)
	cmd := testCommand()
	cmd.RequestHash = ""
	_, err := uow.Run(context.Background(), cmd, func(context.Context, capcontrol.Transaction) (string, error) {
		return `{}`, nil
	})
	if !errors.Is(err, capcontrol.ErrRequestHashRequired) {
		t.Fatalf("missing request hash error = %v, want ErrRequestHashRequired", err)
	}
}

func TestUnitOfWorkRejectsIdempotencyConflict(t *testing.T) {
	uow, _ := newUOWForTest(t)
	cmd := testCommand()
	if _, err := uow.Run(context.Background(), cmd, func(context.Context, capcontrol.Transaction) (string, error) {
		return `{}`, nil
	}); err != nil {
		t.Fatal(err)
	}
	conflict := cmd
	conflict.CommandID = "cmd-2"
	_, err := uow.Run(context.Background(), conflict, func(context.Context, capcontrol.Transaction) (string, error) {
		return `{}`, nil
	})
	if !errors.Is(err, capcontrol.ErrIdempotencyConflict) {
		t.Fatalf("conflict error = %v, want ErrIdempotencyConflict", err)
	}
}

func assertCount(t *testing.T, db *sql.DB, query string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(query).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s = %d, want %d", query, got, want)
	}
}
