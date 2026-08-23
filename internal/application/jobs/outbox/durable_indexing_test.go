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
//  1. TestDurableIndexing_AtomicWrite_CallbackOnce
//     — 1 outbox_events row → ClaimNext + IndexingHandler.Handle →
//     FakeIndexClipper.IndexClip fired EXACTLY 1 time. Replay ClaimNext
//     returns nil (terminal), counter stays at 1. Spec's
//     "write atomico → callback 1 volta".
//
//  2. TestDurableIndexing_IdempotentReplay_CallbackOnce
//     — 2 INSERTs of the SAME event_key → outbox_events has 1 row
//     (UNIQUE(event_key) DO NOTHING suppresses the duplicate) →
//     handler.Handle → IndexClip fired EXACTLY 1 time. A third
//     post-complete INSERT is also a no-op. Spec's "2 delivery stesso
//     aggregate_id → Qdrant riceve 1 sola chiamata".
//
//  3. TestDurableIndexing_SupersededEvent_NoCallback
//     — stale event_payload.source_version differs from the current
//     aggregate's source_version → handler.Handle returns typed
//     *outboxevents.SupersedeError → IndexClip NEVER called. Pin the
//     source_version supersede gate; without it the canonical pipeline
//     would double-write Qdrant with stale embeddings.
//
// Scope decision: the test BYPASSES the Dispatcher (`sqliteoutbox.Dispatcher`)
// and inserts outbox_events rows directly via SQL on a :memory: SQLite.
// Reasons:
//   - The Dispatcher plumbing (medias_assets.upsert + clips-port interfaces
//   - txmanager + IndexStateTxInput struct) is a separate concern.
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
//
//	qdrant_flow_e2e_test.go::TestE2E_RetryAndDeadLetter
//	qdrant_flow_e2e_test.go::TestE2E_LeaseExpiryAndReclaim_WorkerCrash
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

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// ── Schema (mirrors migrations/sqlite/092_create_outbox_events.sql) ─────

// testOutboxSchemaCommitF mirrors production's outbox_events DDL exactly,
// including three details often missed when mirroring:
//
//  1. PARTIAL UNIQUE INDEX form (CREATE UNIQUE INDEX … WHERE …) that the
//     `outboxevents.Repository.Enqueue` SQL relies on for its
//     `ON CONFLICT(event_key) WHERE event_key != ” DO NOTHING` clause.
//     SQLite rejects the inline `UNIQUE(col) WHERE …` table-constraint
//     form (parser error: `near "WHERE": syntax error`), so the partial-
//     uniqueness predicate MUST live in a separate CREATE UNIQUE INDEX
//     statement.
//
//  2. `last_error`, `worker_id`, `lease_id` are NOT NULL DEFAULT ”.
//     `outboxevents.scanEvent` scans these into non-pointer `string`
//     fields on the `outboxevents.Event` struct; NULL would trigger
//     `sql: Scan error on column index N, name "…" converting NULL to
//     string is unsupported`. Production's migration declares these as
//     NOT NULL DEFAULT ” for exactly this reason — test must mirror.
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
    priority        INTEGER NOT NULL DEFAULT 5,
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
// (UNIQUE(event_key) WHERE event_key != ” DO NOTHING).
func insertOutboxEventCF(t *testing.T, db *sql.DB, assetID, sourceVersion, idempotencyKey string) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"schema_version":  outbox.IndexRequestSchemaVersion, // "asset.index.requested.v1"
		"event_id":        idempotencyKey,
		"asset_id":        assetID,
		"operation":       outbox.IndexRequestOperationUPSERT,
		"source_version":  sourceVersion,
		"idempotency_key": idempotencyKey,
		"requested_at":    time.Now().UTC().Format(time.RFC3339),
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
//     event_key != ” DO NOTHING suppresses the duplicate at SQL level;
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

// ── Scenario 4: integration — finalizer → outbox → handler → no supersede ──

// dbSourceQuerier adapts *sql.DB to the SourceVersionQuerier interface
// by delegating to assets.SourceVersionFor — the SAME production SQL
// helper the IndexingHandler uses via *assets.ClipsRepository.
// This eliminates the fakeSourceQuerier drift risk: the test reads from
// the real 3-tier COALESCE chain (content_hash → file_hash → column).
type dbSourceQuerier struct {
	db *sql.DB
}

func (q *dbSourceQuerier) SourceVersionFor(ctx context.Context, id string) (string, error) {
	return assets.SourceVersionFor(ctx, q.db, id)
}

// testCombinedSchema creates media_assets + outbox_events + asset_versions
// + asset_locations tables in a single in-memory SQLite DB. This mirrors
// the real production schema where both the finalizer and the outbox
// handler share the same database.
const testCombinedSchema = `
CREATE TABLE IF NOT EXISTS media_assets (
	id TEXT PRIMARY KEY,
	source TEXT NOT NULL DEFAULT '',
	name TEXT NOT NULL DEFAULT '',
	filename TEXT NOT NULL DEFAULT '',
	media_type TEXT NOT NULL DEFAULT '',
	category TEXT NOT NULL DEFAULT '',
	duration_ms INTEGER NOT NULL DEFAULT 0,
	legacy_file_md5 TEXT NOT NULL DEFAULT '',
	drive_file_id TEXT NOT NULL DEFAULT '',
	drive_link TEXT NOT NULL DEFAULT '',
	download_link TEXT NOT NULL DEFAULT '',
	folder_id TEXT NOT NULL DEFAULT '',
	folder_path TEXT NOT NULL DEFAULT '',
	lifecycle_state TEXT NOT NULL DEFAULT 'ACTIVE',
	index_state TEXT NOT NULL DEFAULT 'DISCOVERED',
	metadata_json TEXT NOT NULL DEFAULT '{}',
	width INTEGER NOT NULL DEFAULT 0,
	height INTEGER NOT NULL DEFAULT 0,
	local_path TEXT NOT NULL DEFAULT '',
	source_provider TEXT NOT NULL DEFAULT '',
	source_version TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL DEFAULT '',
    search_text TEXT NOT NULL DEFAULT '',
    thumbnail_url TEXT NOT NULL DEFAULT '',
    url TEXT NOT NULL DEFAULT '',
    asset_version TEXT NOT NULL DEFAULT '',
    asset_location TEXT NOT NULL DEFAULT '',
    rendition TEXT NOT NULL DEFAULT '',
    source_video_id TEXT NOT NULL DEFAULT '',
    source_url TEXT NOT NULL DEFAULT '',
    start_ms INTEGER NOT NULL DEFAULT 0,
    end_ms INTEGER NOT NULL DEFAULT 0,
    title TEXT NOT NULL DEFAULT '',
    origin TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT '',
    namespace TEXT NOT NULL DEFAULT '',
    asset_kind TEXT NOT NULL DEFAULT '',
    source_type TEXT NOT NULL DEFAULT '',
    semantic_role TEXT NOT NULL DEFAULT '',
    tags TEXT NOT NULL DEFAULT '',
    tags_norm TEXT NOT NULL DEFAULT '',
    drive_folder_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS asset_versions (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	asset_id TEXT NOT NULL REFERENCES media_assets(id) ON DELETE CASCADE,
	version_number INTEGER NOT NULL,
	source_uri TEXT NOT NULL DEFAULT '',
	legacy_file_md5 TEXT NOT NULL DEFAULT '',
	file_size_bytes INTEGER NOT NULL DEFAULT 0,
	mime_type TEXT NOT NULL DEFAULT '',
	metadata_json TEXT NOT NULL DEFAULT '{}',
	created_at TEXT NOT NULL DEFAULT '',
	UNIQUE (asset_id, version_number)
);
CREATE TABLE IF NOT EXISTS asset_locations (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	asset_id TEXT NOT NULL REFERENCES media_assets(id) ON DELETE CASCADE,
	location_kind TEXT NOT NULL CHECK (location_kind IN ('local', 'drive', 'object_storage')),
	uri TEXT NOT NULL,
	external_id TEXT NOT NULL DEFAULT '',
	web_view_link TEXT NOT NULL DEFAULT '',
	download_url TEXT NOT NULL DEFAULT '',
	mime_type TEXT NOT NULL DEFAULT '',
	file_size_bytes INTEGER NOT NULL DEFAULT 0,
	legacy_file_md5 TEXT NOT NULL DEFAULT '',
	is_primary INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL DEFAULT '',
	UNIQUE (asset_id, location_kind)
);
CREATE TABLE IF NOT EXISTS outbox_events (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type      TEXT    NOT NULL,
    aggregate_id    TEXT    NOT NULL,
    aggregate_type  TEXT    NOT NULL DEFAULT '',
    payload_json    TEXT    NOT NULL,
    event_key       TEXT    NOT NULL DEFAULT '',
    status          TEXT    NOT NULL DEFAULT 'pending',
    attempt_count   INTEGER NOT NULL DEFAULT 0,
    max_attempts    INTEGER NOT NULL DEFAULT 5,
    priority        INTEGER NOT NULL DEFAULT 5,
    last_error      TEXT    NOT NULL DEFAULT '',
    worker_id       TEXT    NOT NULL DEFAULT '',
    lease_id        TEXT    NOT NULL DEFAULT '',
    lease_expiry    DATETIME,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    next_attempt_at DATETIME,
    completed_at    DATETIME
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_outbox_events_event_key
    ON outbox_events(event_key) WHERE event_key != '';
CREATE INDEX IF NOT EXISTS IX_outbox_events_status_next
    ON outbox_events(status, next_attempt_at);
`

func openInMemDB_Integration(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:?_busy_timeout=5000&_foreign_keys=on")
	if err != nil {
		t.Fatalf("open :memory: db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(testCombinedSchema); err != nil {
		t.Fatalf("apply combined schema: %v", err)
	}
	return db
}

// TestDurableIndexing_FinalizerWrites_HandlerDoesNotSupersede is the
// canonical integration-level test that closes the loop on the
// content_hash supersede-gate fix (asset_finalizer_tx.go PR, July 2026).
//
// It simulates the PRODUCTION flow end-to-end:
//
//  1. FinalizeAsset writes media_assets row with content_hash in
//     metadata_json (same tx as outbox event creation).
//  2. The outbox event's source_version = artifact.SHA256.
//  3. IndexingHandler reads SourceVersionFor() from the SAME DB
//     (real 3-tier COALESCE: content_hash → file_hash → column).
//  4. Supersede gate MUST NOT fire — content_hash (Tier 1) matches
//     the event's source_version (same write boundary).
//  5. IndexClip MUST be invoked.
//
// Stages 1–5 are happy-path integration coverage: they verify that
// FinalizeAsset writes content_hash to metadata_json and that
// SourceVersionFor reads it correctly through the supersede gate.
//
// Stage 6 is the ACTUAL regression guard: it simulates a different
// pipeline (YouTube) having written a stale file_hash to metadata_json
// in a previous ingest. Without the content_hash fix, SourceVersionFor
// would read the stale Tier 2 (file_hash) and the supersede gate would
// fire — Qdrant never updates. With the fix, Tier 1 (content_hash)
// wins because the finalizer writes it on every republish.
func TestDurableIndexing_FinalizerWrites_HandlerDoesNotSupersede(t *testing.T) {
	db := openInMemDB_Integration(t)
	repo := outboxevents.NewRepository(db)

	fx := finalizer.NewAssetTxFinalizer(zap.NewNop(), assets.NewSQLiteAssetCommitter(db, outboxevents.NewRepository(db), nil))
	ctx := context.Background()
	assetID := "yt_integration_001"

	// ── Stage 1: FinalizeAsset writes media_assets + returns outbox event ──
	hash1 := "sha256:integration_hash_v1"
	artifact1 := finalization.PublishedArtifact{
		ArtifactID:     assetID,
		Kind:           finalization.KindVideo,
		Filename:       "test-video.mp4",
		MIMEType:       "video/mp4",
		SizeBytes:      1024,
		SHA256:         hash1,
		Requirement:    finalization.ArtifactRequirementRequired,
		IdempotencyKey: "idem-" + assetID,
		Location: finalization.AssetLocation{
			Provider:     "drive",
			FileID:       "drive-file-integration",
			WebViewLink:  "https://drive.google.com/file/d/drive-file-integration/view",
			DownloadLink: "https://drive.google.com/uc?id=drive-file-integration",
			FolderID:     "folder-abc",
			FolderPath:   "/test",
			Action:       finalization.PublishCreated,
		},
	}

	tx1, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx1: %v", err)
	}
	ref1, events1, err := fx.FinalizeAsset(ctx, finalizer.WrapTx(tx1), artifact1)
	if err != nil {
		tx1.Rollback()
		t.Fatalf("FinalizeAsset (v1): %v", err)
	}
	if err := tx1.Commit(); err != nil {
		t.Fatalf("commit tx1: %v", err)
	}
	if ref1.ContentHash != hash1 {
		t.Errorf("ref1.ContentHash = %q, want %q", ref1.ContentHash, hash1)
	}

	// Insert the returned outbox event into the outbox_events table
	// (mirrors production: the JobFinalizer inserts events after commit).
	if len(events1) != 1 {
		t.Fatalf("expected 1 outbox event from finalizer, got %d", len(events1))
	}
	eventKey1 := events1[0].EventKey
	_, err = db.Exec(`
		INSERT INTO outbox_events
		    (event_type, aggregate_id, aggregate_type, payload_json, event_key, status)
		VALUES (?, ?, '', ?, ?, 'pending')
		ON CONFLICT(event_key) WHERE event_key != '' DO NOTHING
	`, events1[0].EventType, events1[0].AggregateID, string(events1[0].Payload), eventKey1)
	if err != nil {
		t.Fatalf("insert outbox event (v1): %v", err)
	}

	// ── Stage 2: wire IndexingHandler with REAL SourceVersionFor ──
	clipper := newFakeIndexClipper()
	srcQuerier := &dbSourceQuerier{db: db}
	handler := outbox.NewIndexingHandler(clipper, srcQuerier, zap.NewNop())

	// ── Stage 3: claim + handle — MUST NOT supersede ──
	claim1, err := repo.ClaimNext(ctx, "worker-integration-1", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNext (v1): %v", err)
	}
	if claim1 == nil {
		t.Fatal("expected a claim (1 pending event)")
	}

	err = handler.Handle(ctx, claim1.Event)
	if err != nil {
		var supersede *outboxevents.SupersedeError
		if errors.As(err, &supersede) {
			t.Fatalf("handler.Handle returned SupersedeError — the content_hash fix is BROKEN! (current=%q, expected=%q)",
				supersede.Current, supersede.Expected)
		}
		t.Fatalf("handler.Handle: %v", err)
	}
	if got := clipper.CallCount(assetID); got != 1 {
		t.Errorf("IndexClip(%s) called %d times after FinalizeAsset, want 1", assetID, got)
	}

	// ── Stage 4: REPUBLISH scenario (the original bug) ──
	// Finalize with a NEW hash — this is the exact scenario where the
	// supersede gate would fire if content_hash was missing from
	// metadata_json (stale Tier 2 would beat empty Tier 1).
	hash2 := "sha256:integration_hash_v2"
	artifact2 := artifact1
	artifact2.SHA256 = hash2
	artifact2.IdempotencyKey = "idem-" + assetID + "-v2"

	tx2, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx2: %v", err)
	}
	ref2, events2, err := fx.FinalizeAsset(ctx, finalizer.WrapTx(tx2), artifact2)
	if err != nil {
		tx2.Rollback()
		t.Fatalf("FinalizeAsset (v2): %v", err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatalf("commit tx2: %v", err)
	}
	if ref2.ContentHash != hash2 {
		t.Errorf("ref2.ContentHash = %q, want %q", ref2.ContentHash, hash2)
	}

	// Insert the republish outbox event.
	if len(events2) != 1 {
		t.Fatalf("expected 1 outbox event from republish, got %d", len(events2))
	}
	_, err = db.Exec(`
		INSERT INTO outbox_events
		    (event_type, aggregate_id, aggregate_type, payload_json, event_key, status)
		VALUES (?, ?, '', ?, ?, 'pending')
		ON CONFLICT(event_key) WHERE event_key != '' DO NOTHING
	`, events2[0].EventType, events2[0].AggregateID, string(events2[0].Payload), events2[0].EventKey)
	if err != nil {
		t.Fatalf("insert outbox event (v2): %v", err)
	}

	// Mark the v1 event as completed so ClaimNext picks up the v2 event.
	if err := repo.MarkCompleted(ctx, claim1.Event.ID, claim1.LeaseID); err != nil {
		t.Fatalf("MarkCompleted (v1): %v", err)
	}

	claim2, err := repo.ClaimNext(ctx, "worker-integration-2", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNext (v2): %v", err)
	}
	if claim2 == nil {
		t.Fatal("expected a claim for republish event")
	}

	// THE CRITICAL ASSERTION: the republish event must NOT be superseded.
	// Before the content_hash fix, metadata_json was missing content_hash,
	// so SourceVersionFor read stale Tier 2 (old file_hash from v1 ingest)
	// and the supersede gate fired — Qdrant never got updated.
	err = handler.Handle(ctx, claim2.Event)
	if err != nil {
		var supersede *outboxevents.SupersedeError
		if errors.As(err, &supersede) {
			t.Fatalf("RE-PUBLISH SupersedeError — content_hash fix is BROKEN! (current=%q, expected=%q). \n"+
				"SourceVersionFor should read Tier 1 (content_hash=new-hash) from metadata_json, \n"+
				"NOT stale Tier 2 (file_hash=old-hash) from previous ingest.",
				supersede.Current, supersede.Expected)
		}
		t.Fatalf("handler.Handle (v2): %v", err)
	}
	// IndexClip fired for the republish — Qdrant gets updated.
	if got := clipper.CallCount(assetID); got != 2 {
		t.Errorf("IndexClip(%s) called %d times after republish, want 2 (v1 + v2)", assetID, got)
	}

	// ── Stage 5: verify SourceVersionFor reads Tier 1 (content_hash) ──
	// After the republish, SourceVersionFor must return hash2 (the new
	// content_hash), NOT hash1 (the stale file_hash from v1).
	sv, err := assets.SourceVersionFor(ctx, db, assetID)
	if err != nil {
		t.Fatalf("SourceVersionFor: %v", err)
	}
	if sv != hash2 {
		t.Errorf("SourceVersionFor(%s) = %q, want %q (Tier 1 content_hash must win after republish)",
			assetID, sv, hash2)
	}

	// ── Stage 6: stale-Tier-2 regression guard ──
	// This simulates the ACTUAL production bug: a different pipeline
	// (YouTube) wrote metadata_json.file_hash = "stale_old_hash" in a
	// previous ingest. After the finalizer republishes, ON CONFLICT
	// replaces metadata_json — but without content_hash, Tier 2
	// (stale file_hash) would have beaten Tier 3 (fresh column).
	//
	// The fix ensures Tier 1 (content_hash) wins because the finalizer
	// writes it on every republish. This stage verifies the fix
	// survives even when metadata_json previously contained a stale
	// file_hash from a different pipeline.
	staleHash := "sha256:stale_from_youtube_pipeline"
	hash3 := "sha256:integration_hash_v3"

	// Simulate: YouTube pipeline wrote stale file_hash to metadata_json.
	if _, err := db.Exec(`UPDATE media_assets SET metadata_json = ? WHERE id = ?`,
		`{"file_hash":"`+staleHash+`","publish_action":"drive"}`, assetID); err != nil {
		t.Fatalf("simulate stale Tier 2: %v", err)
	}

	// Verify: before the fix, SourceVersionFor would read stale Tier 2.
	// After the fix, the next FinalizeAsset overwrites metadata_json
	// with content_hash.
	svStale, err := assets.SourceVersionFor(ctx, db, assetID)
	if err != nil {
		t.Fatalf("SourceVersionFor (stale): %v", err)
	}
	if svStale != staleHash {
		t.Fatalf("pre-condition failed: SourceVersionFor = %q, want stale %q (stale Tier 2 simulation didn't work)",
			svStale, staleHash)
	}

	// Now republish with hash3 — the finalizer writes content_hash=hash3
	// to metadata_json, overwriting the stale file_hash.
	artifact3 := artifact1
	artifact3.SHA256 = hash3
	artifact3.IdempotencyKey = "idem-" + assetID + "-v3"

	tx3, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx3: %v", err)
	}
	_, events3, err := fx.FinalizeAsset(ctx, finalizer.WrapTx(tx3), artifact3)
	if err != nil {
		tx3.Rollback()
		t.Fatalf("FinalizeAsset (v3): %v", err)
	}
	if err := tx3.Commit(); err != nil {
		t.Fatalf("commit tx3: %v", err)
	}

	// Verify: SourceVersionFor now returns hash3 (Tier 1 content_hash),
	// NOT staleHash (the stale Tier 2 file_hash that was overwritten).
	sv3, err := assets.SourceVersionFor(ctx, db, assetID)
	if err != nil {
		t.Fatalf("SourceVersionFor (v3): %v", err)
	}
	if sv3 != hash3 {
		t.Errorf("SourceVersionFor after republish with stale Tier 2 = %q, want %q (content_hash must overwrite stale file_hash)",
			sv3, hash3)
	}

	// Insert v3 outbox event and verify handler does NOT supersede.
	if len(events3) != 1 {
		t.Fatalf("expected 1 outbox event from v3, got %d", len(events3))
	}
	_, err = db.Exec(`
		INSERT INTO outbox_events
		    (event_type, aggregate_id, aggregate_type, payload_json, event_key, status)
		VALUES (?, ?, '', ?, ?, 'pending')
		ON CONFLICT(event_key) WHERE event_key != '' DO NOTHING
	`, events3[0].EventType, events3[0].AggregateID, string(events3[0].Payload), events3[0].EventKey)
	if err != nil {
		t.Fatalf("insert outbox event (v3): %v", err)
	}
	if err := repo.MarkCompleted(ctx, claim2.Event.ID, claim2.LeaseID); err != nil {
		t.Fatalf("MarkCompleted (v2): %v", err)
	}

	claim3, err := repo.ClaimNext(ctx, "worker-integration-3", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNext (v3): %v", err)
	}
	if claim3 == nil {
		t.Fatal("expected a claim for v3 republish event")
	}

	// THE STALE-TIER-2 REGRESSION GUARD: before the fix, SourceVersionFor
	// would read staleHash from Tier 2 (stale file_hash), compare with
	// hash3 from the event, and mark as superseded. After the fix,
	// Tier 1 (content_hash=hash3) matches the event — no supersede.
	err = handler.Handle(ctx, claim3.Event)
	if err != nil {
		var supersede *outboxevents.SupersedeError
		if errors.As(err, &supersede) {
			t.Fatalf("STALE-TIER-2 SupersedeError — content_hash fix is BROKEN! (current=%q, expected=%q). \n"+
				"Before the fix: Tier 2 (stale file_hash=%q) beat empty Tier 1. \n"+
				"After the fix: Tier 1 (content_hash=%q) wins.",
				supersede.Current, supersede.Expected, staleHash, hash3)
		}
		t.Fatalf("handler.Handle (v3): %v", err)
	}
	if got := clipper.CallCount(assetID); got != 3 {
		t.Errorf("IndexClip(%s) called %d times after stale-Tier-2 scenario, want 3 (v1 + v2 + v3)", assetID, got)
	}
}
