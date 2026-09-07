package jobs

import (
	"context"
	"database/sql"
	"errors"
	assets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesregistry"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
	"testing"
	"time"
)

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
	handler := NewIndexingHandler(clipper, nil, zap.NewNop())

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
	handler := NewIndexingHandler(clipper, nil, zap.NewNop())

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
	handler := NewIndexingHandler(clipper, srcQuerier, zap.NewNop())

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
