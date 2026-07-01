// Package outbox_test — durable_indexing_test.go (Commit F, June 2026)
//
// Consumer-side integration test contract for the canonical durable indexing
// pipeline:
//
//	process_segment.go::Step 9
//	  → youtubeports.ClipAtomicWriter.CommitClipAndIndexEvent
//	    → outbox_events row (event_key = clipID, UNIQUE)
//	      → outboxevents.Pool (worker, async, retry with pkg/retry)
//	        → IndexingHandler.Handle
//	          → IndexClip(ctx, clipID) → Qdrant upsert
//
// Spec (June 2026, Commit F, Italian, verbatim):
// > "Outbox dispatcher consuma async, idempotente su (aggregate_id, type),
// > retry con pkg/retry, failure terminale → log+drop o dead letter.
// > Elimina ogni triggerAutoIndexing/IndexClip fire-and-forget.
// > Test: SQLite in-memory con callback counter, write atomico → callback
// > chiamata 1 volta. Test idempotenza: 2 delivery stesso aggregate_id
// > → Qdrant riceve 1 sola chiamata."
//
// Three scenarios pinned:
//
//   1. TestDurableIndexing_AtomicWrite_CallbackOnce
//      — 1 outbox_events row → ClaimNext + IndexingHandler.Handle →
//        FakeIndexClipper.IndexClip fired EXACTLY 1 time. Replay ClaimNext
//        returns nil (terminal), counter stays at 1. Spec's
//        "write atomico → callback 1 volta".
//
//   2. TestDurableIndexing_IdempotentReplay_CallbackOnce
//      — 2 INSERTs of the SAME event_key → outbox_events has 1 row
//        (UNIQUE(event_key) DO NOTHING suppresses the duplicate) →
//        handler.Handle → IndexClip fired EXACTLY 1 time. A third
//        post-complete INSERT is also a no-op. Spec's "2 delivery stesso
//        aggregate_id → Qdrant riceve 1 sola chiamata".
//
//   3. TestDurableIndexing_SupersededEvent_NoCallback
//      — stale event_payload.source_version differs from the current
//        aggregate's source_version → handler.Handle returns typed
//        *outboxevents.SupersedeError → IndexClip NEVER called. Pin the
//        source_version supersede gate; without it the canonical pipeline
//        would double-write Qdrant with stale embeddings.
//
// Scope decision: the test BYPASSES the Dispatcher (`sqliteoutbox.Dispatcher`)
// and inserts outbox_events rows directly via SQL on a :memory: SQLite.
// Reasons:
//   - The Dispatcher plumbing (medias_assets.upsert + clips-port interfaces
//     + txmanager + IndexStateTxInput struct) is a separate concern.
//   - The "atomic write" property of `media_assets.upsert + outbox_events.insert`
//     in a single tx is already pinned by
//     internal/infrastructure/database/sqlite/outbox/qdrant_flow_e2e_test.go
//     (TestE2E_HappyPath_EnqueueProcessComplete) AND by
//     internal/infrastructure/database/sqlite/youtube/clip_atomic_writer_test.go
//     (the Commit F producer-side regression).
//   - What THIS file uniquely pins is the **callback-count contract**:
//     1 outbox_events row → 1 IndexClip call. Bypassing the Dispatcher
//     makes the test sharper and avoids the import cycle that
//     `assets → application/jobs/outbox` introduces if the test sat
//     inside `package outbox` (resolving to `package outbox_test` and
//     dropping the Dispatcher import is the canonical Go pattern).
//
// Pool bypass: synchronous ClaimNext + handler.Handle inline. The Pool
// runtime contract (worker loop, retry, dead-letter) is pinned at:
//   qdrant_flow_e2e_test.go::TestE2E_RetryAndDeadLetter
//   qdrant_flow_e2e_test.go::TestE2E_LeaseExpiryAndReclaim_WorkerCrash
package outbox_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// ── Schema (mirrors migrations/sqlite/092_create_outbox_events.sql) ─────

// testOutboxSchemaCommitF mirrors production's outbox_events DDL exactly,
// including three details often missed when mirroring:
//
//  1. PARTIAL UNIQUE INDEX form (CREATE UNIQUE INDEX … WHERE …) that the
//     `outboxevents.Repository.Enqueue` SQL relies on for its
//     `ON CONFLICT(event_key) WHERE event_key != '' DO NOTHING` clause.
//     SQLite rejects the inline `UNIQUE(col) WHERE …` table-constraint
//     form (parser error: `near "WHERE": syntax error`), so the partial-
//     uniqueness predicate MUST live in a separate CREATE UNIQUE INDEX
//     statement.
//
//  2. `last_error`, `worker_id`, `lease_id` are NOT NULL DEFAULT ''.
//     `outboxevents.scanEvent` scans these into non-pointer `string`
//     fields on the `outboxevents.Event` struct; NULL would trigger
//     `sql: Scan error on column index N, name "…" converting NULL to
//     string is unsupported`. Production's migration declares these as
//     NOT NULL DEFAULT '' for exactly this reason — test must mirror.
//
//  3. `lease_expiry`, `completed_at`, `next_attempt_at` are nullable.
//     scanEvent uses `sql.NullString` for lease_expiry / completed_at
//     (parsed into *time.Time); next_attempt_at is not scanned by
//     ClaimNext's refetch so it stays naked in the schema.
const testOutboxSchemaCommitF = `
CREATE TABLE outbox_events (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type      TEXT    NOT NULL,
    aggregate_id    TEXT    NOT NULL,
    aggregate_type  TEXT    NOT NULL DEFAULT '',
    payload_json    TEXT    NOT NULL,
    event_key       TEXT    NOT NULL DEFAULT '',
    status          TEXT    NOT NULL DEFAULT 'pending',
    attempt_count   INTEGER NOT NULL DEFAULT 0,
    max_attempts    INTEGER NOT NULL DEFAULT 5,
    last_error      TEXT    NOT NULL DEFAULT '',
    worker_id       TEXT    NOT NULL DEFAULT '',
    lease_id        TEXT    NOT NULL DEFAULT '',
    lease_expiry    DATETIME,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    next_attempt_at DATETIME,
    completed_at    DATETIME
);
CREATE UNIQUE INDEX ux_outbox_events_event_key
    ON outbox_events(event_key) WHERE event_key != '';
CREATE INDEX IX_outbox_events_status_next
    ON outbox_events(status, next_attempt_at);
`

// ── Fakes ──────────────────────────────────────────────────────────────

// fakeIndexClipper captures IndexClip invocations per clipID. The single
// goroutine in each test makes the mutex over-engineered at present, but
// is kept deliberately so a future async-driver variant (Pool worker test)
// cannot introduce a silent regression. Matches the canonical PipelineGen
// style of leaving sync.Mutex on test fakes by default.
type fakeIndexClipper struct {
	mu    sync.Mutex
	calls map[string]int
}

func newFakeIndexClipper() *fakeIndexClipper {
	return &fakeIndexClipper{calls: make(map[string]int)}
}

func (f *fakeIndexClipper) IndexClip(ctx context.Context, clipID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[clipID]++
	return nil
}

func (f *fakeIndexClipper) CallCount(clipID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[clipID]
}

// fakeSourceQuerier returns the configured source_version for the given
// aggregate id, or sql.ErrNoRows for unknowns (matches the canonical
// SourceVersionQuerier contract pinned in
// internal/infrastructure/database/sqlite/assets/source_version_test.go).
type fakeSourceQuerier struct {
	versions map[string]string
}

func (f *fakeSourceQuerier) SourceVersionFor(ctx context.Context, id string) (string, error) {
	v, ok := f.versions[id]
	if !ok {
		return "", sql.ErrNoRows
	}
	return v, nil
}

// ── Helpers ────────────────────────────────────────────────────────────

func openInMemDB_CF(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:?_busy_timeout=5000&_foreign_keys=on")
	if err != nil {
		t.Fatalf("open :memory: db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(testOutboxSchemaCommitF); err != nil {
		t.Fatalf("apply test schema: %v", err)
	}
	return db
}

// insertOutboxEventCF inserts a synthetic asset.index.requested event
// matching the canonical v1 envelope (schema_version =
// "asset.index.requested.v1"). event_key uses the {event_type}:{asset_id}:{hash}
// key shape that mirrors what the production Dispatcher writes its UNIQUE
// over. Spec contract: calling this twice with the SAME key results in 1 row
// (UNIQUE(event_key) WHERE event_key != '' DO NOTHING).
func insertOutboxEventCF(t *testing.T, db *sql.DB, assetID, sourceVersion, idempotencyKey string) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"schema_version": outbox.IndexRequestSchemaVersion, // "asset.index.requested.v1"
		"event_id":       idempotencyKey,
		"asset_id":       assetID,
		"operation":      outbox.IndexRequestOperationUPSERT,
		"source_version": sourceVersion,
		"idempotency_key": idempotencyKey,
		"requested_at":   time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("marshal v1 envelope: %v", err)
	}
	eventKey := outboxevents.EventAssetIndexRequested + ":" + assetID + ":" + sourceVersion
	_, err = db.Exec(`
		INSERT INTO outbox_events
		    (event_type, aggregate_id, aggregate_type, payload_json, event_key, status)
		VALUES (?, ?, 'youtube', ?, ?, 'pending')
		ON CONFLICT(event_key) WHERE event_key != '' DO NOTHING
	`, outboxevents.EventAssetIndexRequested, assetID, string(payload), eventKey)
	if err != nil {
		t.Fatalf("insert outbox_events(asset=%s): %v", assetID, err)
	}
}

func countOutbox_CF(t *testing.T, db *sql.DB, where string, args ...any) int {
	t.Helper()
	q := "SELECT COUNT(*) FROM outbox_events"
	if where != "" {
		q += " WHERE " + where
	}
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("count outbox_events: %v", err)
	}
	return n
}

func statusOf_CF(t *testing.T, db *sql.DB, eventKey string) string {
	t.Helper()
	var status string
	if err := db.QueryRow(
		"SELECT status FROM outbox_events WHERE event_key = ?", eventKey,
	).Scan(&status); err != nil {
		t.Fatalf("read status for event_key=%q: %v", eventKey, err)
	}
	return status
}

// ── Scenario 1: atomic write → callback called ONCE ────────────────────

// TestDurableIndexing_AtomicWrite_CallbackOnce pins the spec invariant
// "write atomico → callback chiamata 1 volta":
//
//  1. Single outbox_events row inserted (mirrors the atomic
//     media_assets.upsert + outbox_events.insert that production's
//     ClipAtomicWriter does in a single SQLite tx — production path
//     pinned in clip_atomic_writer_test.go).
//  2. Repository.ClaimNext + IndexingHandler.Handle synchronously
//     execute → FakeIndexClipper.IndexClip is called for that
//     clipID EXACTLY 1 time.
//  3. Repository.MarkCompleted transitions status='completed'. A
//     second ClaimNext returns nil (terminal already), and the
//     counter stays at 1 — never 2.
func TestDurableIndexing_AtomicWrite_CallbackOnce(t *testing.T) {
	db := openInMemDB_CF(t)
	repo := outboxevents.NewRepository(db)

	clipper := newFakeIndexClipper()
	// sourceQuerier=nil → supersede gate bypassed, IndexClip is invoked
	// unconditionally. Production wires SourceVersionQuerier; the gate is
	// separately pinned by Scenario 3 below.
	handler := outbox.NewIndexingHandler(clipper, nil, zap.NewNop())

	ctx := context.Background()
	assetID := "yt_commitF_atomic_001"
	hash := "sha256:commitatomic0001"

	// ── Stage 1: single atomic write (one outbox_events row) ──
	insertOutboxEventCF(t, db, assetID, hash, assetID)
	if n := countOutbox_CF(t, db, ""); n != 1 {
		t.Fatalf("after insert: expected 1 outbox_events row, got %d", n)
	}

	// ── Stage 2: worker claim + handler dispatch (pool bypassed) ──
	claim, err := repo.ClaimNext(ctx, "worker-commitF-1", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if claim == nil {
		t.Fatal("expected a claim (1 pending event)")
	}
	if claim.Event.AggregateID != assetID {
		t.Errorf("claim aggregate_id: want %q got %q", assetID, claim.Event.AggregateID)
	}
	if claim.Event.EventType != outboxevents.EventAssetIndexRequested {
		t.Errorf("claim event_type: want %q got %q",
			outboxevents.EventAssetIndexRequested, claim.Event.EventType)
	}

	// ── Stage 3: handler.Handle fires IndexClip EXACTLY 1 time ──
	if err := handler.Handle(ctx, claim.Event); err != nil {
		t.Fatalf("handler.Handle: %v", err)
	}
	if got := clipper.CallCount(assetID); got != 1 {
		t.Errorf("IndexClip(%s) called %d times after 1 atomic write, want 1 (spec: callback 1 volta)",
			assetID, got)
	}

	// ── Stage 4: MarkCompleted (terminal) → no second pickup, no second callback ──
	if err := repo.MarkCompleted(ctx, claim.Event.ID, claim.LeaseID); err != nil {
		t.Fatalf("MarkCompleted: %v", err)
	}
	if got := statusOf_CF(t, db, claim.Event.EventKey); got != "completed" {
		t.Errorf("status: want %q got %q (MarkCompleted should set status='completed')",
			"completed", got)
	}

	// Replay ClaimNext must return nil (terminal already). The Pool worker
	// relies on this so a completed event never re-enters the dispatch loop.
	claim2, err := repo.ClaimNext(ctx, "worker-commitF-1-replay", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNext (replay): %v", err)
	}
	if claim2 != nil {
		t.Errorf("replay ClaimNext: expected nil (status=completed), got claim for aggregate_id=%q",
			claim2.Event.AggregateID)
	}
	// Counter still 1, never 2 — exactly the spec invariant.
	if got := clipper.CallCount(assetID); got != 1 {
		t.Errorf("IndexClip(%s) called %d times after replay (should still be 1), want 1",
			assetID, got)
	}
}

// ── Scenario 2: idempotent replay → callback called ONCE ───────────────

// TestDurableIndexing_IdempotentReplay_CallbackOnce pins the spec invariant
// "2 delivery stesso aggregate_id → Qdrant riceve 1 sola chiamata":
//
//  1. First INSERT of event_key=`asset.index.requested:<id>:<hash>` → 1 row
//     in outbox_events.
//  2. Second INSERT with SAME event_key → UNIQUE(event_key) WHERE
//     event_key != '' DO NOTHING suppresses the duplicate at SQL level;
//     outbox_events STILL has 1 row.
//  3. ClaimNext + IndexingHandler.Handle → IndexClip fired EXACTLY 1 time
//     (not 2). Qdrant receives exactly 1 upsert call. (Additional belt:
//     Qdrant's native uuid5(point_id) upsert idempotency holds even if the
//     SQL dedup ever fails — defence in depth.)
//  4. A third INSERT post-completion STILL stays a no-op — the partial
//     UNIQUE index covers the entire table regardless of status, so
//     already-completed rows still suppress late duplicates.
func TestDurableIndexing_IdempotentReplay_CallbackOnce(t *testing.T) {
	db := openInMemDB_CF(t)
	repo := outboxevents.NewRepository(db)

	clipper := newFakeIndexClipper()
	handler := outbox.NewIndexingHandler(clipper, nil, zap.NewNop())

	ctx := context.Background()
	assetID := "yt_commitF_idempotent_001"
	hash := "sha256:commitreplay0001"

	// ── Stage 1: first insert ──
	insertOutboxEventCF(t, db, assetID, hash, assetID)
	if n := countOutbox_CF(t, db, ""); n != 1 {
		t.Fatalf("after #1: expected 1 outbox_events row, got %d", n)
	}

	// ── Stage 2: second insert SAME event_key → SQL dedup ──
	insertOutboxEventCF(t, db, assetID, hash, assetID)
	if n := countOutbox_CF(t, db, ""); n != 1 {
		t.Errorf("after #2 (replay): expected 1 outbox_events row (UNIQUE event_key suppresses), got %d", n)
	}

	// ── Stage 3: claim + handle ──
	claim, err := repo.ClaimNext(ctx, "worker-commitF-2", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if claim == nil {
		t.Fatal("expected a claim (1 pending event)")
	}
	if claim.Event.AggregateID != assetID {
		t.Errorf("claim aggregate_id: want %q got %q", assetID, claim.Event.AggregateID)
	}

	// ── Stage 4: handler.Handle fires IndexClip EXACTLY 1 time (not 2) ──
	// This is the spec's central invariant. Two inserts of the same event_key
	// must NOT translate to two Qdrant upserts.
	if err := handler.Handle(ctx, claim.Event); err != nil {
		t.Fatalf("handler.Handle: %v", err)
	}
	if got := clipper.CallCount(assetID); got != 1 {
		t.Errorf("IndexClip(%s) called %d times after 2 inserts of same aggregate_id (idempotent replay), want 1",
			assetID, got)
	}

	// ── Stage 5: MarkCompleted (terminal) ──
	if err := repo.MarkCompleted(ctx, claim.Event.ID, claim.LeaseID); err != nil {
		t.Fatalf("MarkCompleted: %v", err)
	}

	// ── Stage 6: a third insert with the SAME hash stays a no-op even after completion ──
	// The partial UNIQUE(event_key) WHERE event_key != '' index covers the
	// entire table regardless of status, so a "completed" row from a prior
	// insert still suppresses later same-key inserts. This is the contract
	// that prevents late duplicates from re-firing IndexClip for already-
	// indexed clips.
	insertOutboxEventCF(t, db, assetID, hash, assetID)
	if n := countOutbox_CF(t, db, ""); n != 1 {
		t.Errorf("post-complete replay: outbox_events must still hold 1 row (UNIQUE suppresses), got %d", n)
	}
	if got := clipper.CallCount(assetID); got != 1 {
		t.Errorf("IndexClip(%s) post-complete replay: want 1, got %d",
			assetID, got)
	}
}

// ── Scenario 3: SUPERSEDED event → no IndexClip callback ───────────────

// TestDurableIndexing_SupersededEvent_NoCallback pins the source_version
// supersede gate: a STALE event must NOT fire IndexClip (Qdrant must NOT
// receive a re-index for a newer-aggregate-version event). The spec's
// "Elimina ogni triggerAutoIndexing/IndexClip fire-and-forget" implies the
// canonical pipeline also short-circuits on supersede — otherwise superseded
// events would still drive spurious Qdrant upserts, contravening the gate.
func TestDurableIndexing_SupersededEvent_NoCallback(t *testing.T) {
	db := openInMemDB_CF(t)
	repo := outboxevents.NewRepository(db)

	clipper := newFakeIndexClipper()
	// SourceVersionQuerier returns a NEWER source_version than the event's
	// claim → IndexingHandler's supersede gate MUST fire.
	srcQuerier := &fakeSourceQuerier{versions: map[string]string{
		"yt_supersede_001": "sha256:newerversionsha",
	}}
	handler := outbox.NewIndexingHandler(clipper, srcQuerier, zap.NewNop())

	ctx := context.Background()
	assetID := "yt_supersede_001"
	staleHash := "sha256:olderversionsha"

	// Insert a STALE event whose current aggregate source_version is "newerversionsha".
	insertOutboxEventCF(t, db, assetID, staleHash, assetID)

	claim, err := repo.ClaimNext(ctx, "worker-commitF-3", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if claim == nil {
		t.Fatal("expected a claim (1 pending event)")
	}

	err = handler.Handle(ctx, claim.Event)
	if err == nil {
		t.Errorf("superseded event: handler.Handle should return non-nil error (SupersedeError), got nil")
	} else {
		var supersede *outboxevents.SupersedeError
		if !errors.As(err, &supersede) {
			t.Errorf("superseded event: handler.Handle error type: want *outboxevents.SupersedeError, got %T (%v)", err, err)
		}
	}

	// THE CENTRAL INVARIANT: IndexClip must NOT be invoked for a superseded
	// event. Otherwise the canonical pipeline would double-write Qdrant with
	// a stale embedding — exactly the bug the spec forbids.
	if got := clipper.CallCount(assetID); got != 0 {
		t.Errorf("IndexClip(%s) called %d times on SUPERSEDED event, want 0 (Qdrant must NOT re-upsert stale data)",
			assetID, got)
	}
}
