// Package outbox — dispatcher_delete_test.go (Blocco 3.2 commit 1/2, June 2026)
//
// Pins the canonical body of Dispatcher.EnqueueDriveDelete + its
// shim Dispatcher.EnqueueAndDelete. The contract is:
//
//	tx body, single commit:
//	  1. UPDATE media_assets SET lifecycle_state=DELETE_REQUESTED, updated_at=<now>
//	       WHERE id=? AND lifecycle_state NOT IN (<deletion-chain states>)
//	  2. INSERT outbox_events (event_type=EventAssetDriveDeleteRequested,
//	                            aggregate_id=assetID,
//	                            aggregate_type="media_asset",
//	                            event_key=drive_delete:<permanently>:<assetID>)
//	       carrying a v1 envelope with schema_version == DriveDeleteRequestSchemaVersion
//
// Tests in this file validate ALL of:
//   - Round-trip v1 envelope shape (SchemaVersion, EventID, AssetID,
//     Permanently, IdempotencyKey == event_key conflation invariant,
//     RequestedAt is RFC3339-shaped).
//   - media_assets row stamp (lifecycle_state flips to DELETE_REQUESTED
//     only on rows NOT already in the deletion chain).
//   - updated_at is bumped on every state flip — the prerequisite
//     for the Blocco 3.2 DeletionReconciler's
//     `WHERE updated_at < now-threshold` query. Without this stamp,
//     the reconciler's stuck-row detection would return every row
//     whose lifecycle_state is in the deletion chain (because
//     updated_at would still reflect the original INSERT timestamp).
//   - Skip-on-already-in-chain: a row already in
//     {DELETE_REQUESTED, DELETE_PENDING, DRIVE_DELETE_PENDING,
//     INDEX_DELETE_PENDING, DELETED} is left untouched by the UPDATE
//     (0 rows affected); the outbox event still inserts because the
//     dispatcher always emits the event in the tx (caller-side ON
//     CONFLICT absorbs the repeat event_key at the outbox layer).
//   - Fail-fast guards: nil *Dispatcher, nil txmgr, nil outboxEventsRepo,
//     empty assetID each reject BEFORE opening a transaction.
//
// Mirrors delete_envelope_test.go's txMgrCapture pattern so the
// tests run against an in-memory *sql.DB and observe row-level state
// without a full media_assets migration fixture.
package outbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"go.uber.org/zap"

	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// minimalMediaAssetsFixture creates the lean media_assets table the
// tests use to observe the lifecycle_state + updated_at write.
// Excludes every column the dispatcher doesn't read — only the
// (id, lifecycle_state, created_at, updated_at) quartet is
// material to the EnqueueDriveDelete contract. The repo fixtures
// from package assets (canonical production schema) are too heavy
// for the unit-test boundary here.
func minimalMediaAssetsFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS media_assets (
			id              TEXT PRIMARY KEY,
			lifecycle_state TEXT NOT NULL DEFAULT 'ACTIVE',
			created_at      TEXT NOT NULL DEFAULT '',
			updated_at      TEXT NOT NULL DEFAULT '',
    filename TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    index_state TEXT NOT NULL DEFAULT '',
    search_text TEXT NOT NULL DEFAULT '',
    source_version TEXT NOT NULL DEFAULT '',
    thumbnail_url TEXT NOT NULL DEFAULT '',
    url TEXT NOT NULL DEFAULT '',
    asset_version TEXT NOT NULL DEFAULT '',
    asset_location TEXT NOT NULL DEFAULT '',
    rendition TEXT NOT NULL DEFAULT '',
    source_provider TEXT NOT NULL DEFAULT '',
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
    drive_folder_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '')
	`)
	if err != nil {
		t.Fatalf("create media_assets fixture: %v", err)
	}
}

// insertRow inserts a single media_assets row with the given
// (id, state, updated_at). Tests use this to seed pre-stamp
// states ("ACTIVE row in tests, then EnqueueDriveDelete") and
// to encode the pre-deletion-chain states for the skip test.
func insertRow(t *testing.T, db *sql.DB, id string, state string, updatedAt string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO media_assets (id, lifecycle_state, created_at, updated_at) VALUES (?, ?, ?, ?)`, id, state, updatedAt, updatedAt)
	if err != nil {
		t.Fatalf("insert row %s: %v", id, err)
	}
}

// TestEnqueueDriveDelete_StampsDELETE_REQUESTEDAndEmitsV1Envelope
// is the body-pin for the canonical happy path.
//
// Asserts:
//   - lifecycle_state flips to DELETE_REQUESTED on an ACTIVE row.
//   - updated_at is bumped (prerequisite for reconciler
//     threshold queries; this would fail prior to Blocco 3.2
//     commit 1's fix).
//   - outbox row inserted with event_type=EventAssetDriveDeleteRequested,
//     aggregate_id=assetID, aggregate_type="media_asset",
//     event_key=drive_delete:false:<assetID>.
//   - v1 payload round-trips through canonical shape
//     (SchemaVersion == DriveDeleteRequestSchemaVersion,
//     AssetID == id, IdempotencyKey == event_key).
//   - Permanently=false propagates into the event_key + payload.
func TestEnqueueDriveDelete_StampsDELETE_REQUESTEDAndEmitsV1Envelope(t *testing.T) {
	db := memoryDB(t)
	ensureOutboxSchema(t, db)
	minimalMediaAssetsFixture(t, db)

	originalUpdatedAt := "2000-01-01T00:00:00Z"
	insertRow(t, db, "asset_xyz", "ACTIVE", originalUpdatedAt)

	beforeCall := time.Now().UTC()
	d := NewDispatcher(&fakeClips{}, &fakeClips{}, outboxevents.NewRepository(db), &txMgrCapture{db: db}, zap.NewNop())
	const assetID = "asset_xyz"
	if err := d.EnqueueDriveDelete(context.Background(), assetID, false); err != nil {
		t.Fatalf("EnqueueDriveDelete: %v", err)
	}
	afterCall := time.Now().UTC()

	// 1. lifecycle_state flipped to DELETE_REQUESTED.
	var state string
	var updatedAt string
	if err := db.QueryRow(`SELECT lifecycle_state, updated_at FROM media_assets WHERE id = ?`, assetID).Scan(&state, &updatedAt); err != nil {
		t.Fatalf("read media_assets row: %v", err)
	}
	if state != "DELETE_REQUESTED" {
		t.Errorf("lifecycle_state: want DELETE_REQUESTED got %q", state)
	}

	// 2. updated_at was bumped (and is between beforeCall and afterCall).
	got := timeutil.ParseRFC3339(updatedAt)
	if got.IsZero() {
		t.Fatalf("parse updated_at %q returned zero time", updatedAt)
	}
	if !got.After(beforeCall.Add(-time.Second)) || got.After(afterCall.Add(time.Second)) {
		t.Errorf("updated_at %s not within ±1s of call window [%s, %s]", got, beforeCall, afterCall)
	}
	if updatedAt == originalUpdatedAt {
		t.Error("updated_at must change on lifecycle_state flip (would break DeletionReconciler stuck-row query)")
	}

	// 3. outbox row shape.
	var eventType, aggID, aggType, payload, eventKey string
	if err := db.QueryRow(`SELECT event_type, aggregate_id, aggregate_type, payload_json, event_key FROM outbox_events ORDER BY id DESC LIMIT 1`).Scan(&eventType, &aggID, &aggType, &payload, &eventKey); err != nil {
		t.Fatalf("scan outbox row: %v", err)
	}
	if eventType != outboxevents.EventAssetDriveDeleteRequested {
		t.Errorf("event_type: want %q got %q", outboxevents.EventAssetDriveDeleteRequested, eventType)
	}
	if aggID != assetID {
		t.Errorf("aggregate_id: want %q got %q", assetID, aggID)
	}
	if aggType != "media_asset" {
		t.Errorf("aggregate_type: want media_asset got %q", aggType)
	}
	if eventKey != "drive_delete:false:"+assetID {
		t.Errorf("event_key: want drive_delete:false:%s got %q", assetID, eventKey)
	}

	// 4. v1 envelope body shape.
	var p driveDeleteRequestV1
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		t.Fatalf("json.Unmarshal v1 envelope: %v\npayload: %s", err, payload)
	}
	if p.SchemaVersion != DriveDeleteRequestSchemaVersion {
		t.Errorf("schema_version: want %q got %q", DriveDeleteRequestSchemaVersion, p.SchemaVersion)
	}
	if p.AssetID != assetID {
		t.Errorf("asset_id: want %q got %q", assetID, p.AssetID)
	}
	if p.EventID == "" {
		t.Error("event_id must be non-empty (operator audit UUID)")
	}
	if p.Permanently {
		t.Error("permanently must be false (test passed permanently=false)")
	}
	if p.IdempotencyKey != eventKey {
		t.Errorf("v1 conflation invariant: payload.IdempotencyKey (%q) != event_key (%q)", p.IdempotencyKey, eventKey)
	}
	if p.RequestedAt == "" {
		t.Error("requested_at: must be non-empty RFC3339 (operator audit)")
	}
}

// TestEnqueueDriveDelete_PermanentlyTrueVerifiesKeyDistinction pins
// that `permanently=true` produces a DIFFERENT event_key from
// `permanently=false`. Operators rapidly toggling permanently
// between requests must see both events delivered as separate hops;
// this is the v1 conflation invariant in practice.
func TestEnqueueDriveDelete_PermanentlyTrueVerifiesKeyDistinction(t *testing.T) {
	db := memoryDB(t)
	ensureOutboxSchema(t, db)
	minimalMediaAssetsFixture(t, db)
	insertRow(t, db, "asset_perm", "ACTIVE", "2000-01-01T00:00:00Z")

	d := NewDispatcher(&fakeClips{}, &fakeClips{}, outboxevents.NewRepository(db), &txMgrCapture{db: db}, zap.NewNop())
	if err := d.EnqueueDriveDelete(context.Background(), "asset_perm", true); err != nil {
		t.Fatalf("EnqueueDriveDelete permanently=true: %v", err)
	}

	var eventKey string
	if err := db.QueryRow(`SELECT event_key FROM outbox_events ORDER BY id DESC LIMIT 1`).Scan(&eventKey); err != nil {
		t.Fatalf("scan event_key: %v", err)
	}
	if eventKey != "drive_delete:true:asset_perm" {
		t.Errorf("event_key: want drive_delete:true:asset_perm got %q", eventKey)
	}
}

// TestEnqueueDriveDelete_StampsUpdatedAtOnLifecycleFlip is the
// dedicated regression test for the Blocco 3.2 commit 1 fix:
// `updated_at = <now>` is set on the lifecycle_state flip.
//
// Without the fix: SQLite's CURRENT_TIMESTAMP default ONLY fires on
// INSERT; subsequent UPDATEs leave updated_at at the row's
// original INSERT timestamp. The DeletionReconciler's
// "WHERE lifecycle_state IN (..) AND updated_at < now-threshold"
// query would then return every deletion-chain row (because
// updated_at still reflects the row's INSERT time, not the flip
// time). With the fix: updated_at is bumped on every flip and
// the reconciler's threshold query is meaningful.
//
// Direct string comparison of the seed vs the post-call value
// (rather than a parsed-time roundtrip via timeutil.FormatRFC3339
// vs time.RFC3339Nano coupling): keeps the assertion deterministic
// across the FormatRFC3339 implementation variants (RFC3339 /
// RFC3339Nano) and avoids any parse-format-mismatch false positive.
func TestEnqueueDriveDelete_StampsUpdatedAtOnLifecycleFlip(t *testing.T) {
	db := memoryDB(t)
	ensureOutboxSchema(t, db)
	minimalMediaAssetsFixture(t, db)

	originalUpdatedAt := "2020-01-01T00:00:00Z"
	insertRow(t, db, "asset_old", "ACTIVE", originalUpdatedAt)

	d := NewDispatcher(&fakeClips{}, &fakeClips{}, outboxevents.NewRepository(db), &txMgrCapture{db: db}, zap.NewNop())
	if err := d.EnqueueDriveDelete(context.Background(), "asset_old", false); err != nil {
		t.Fatalf("EnqueueDriveDelete: %v", err)
	}

	var updatedAt string
	if err := db.QueryRow(`SELECT updated_at FROM media_assets WHERE id = ?`, "asset_old").Scan(&updatedAt); err != nil {
		t.Fatalf("read updated_at: %v", err)
	}
	if updatedAt == originalUpdatedAt {
		t.Fatalf("updated_at was not bumped (still %q) — DBR fix not applied", updatedAt)
	}
	// Defensive sanity: the new value is parseable as RFC3339.
	if got := timeutil.ParseRFC3339(updatedAt); got.IsZero() {
		t.Errorf("updated_at %q is not parseable as RFC3339 (zero value)", updatedAt)
	}
}

// TestEnqueueDriveDelete_SkipsAlreadyInFlightRow is the boundary
// test for the WHERE-NOT-IN clause:
//
//	lifecycle_state NOT IN ('DELETE_REQUESTED', 'DELETE_PENDING',
//	                          'DRIVE_DELETE_PENDING',
//	                          'INDEX_DELETE_PENDING', 'DELETED')
//
// A row already in any of those 5 states is left untouched (0
// UPDATE rows; the dispatcher's outbox event still inserts because
// the dispatcher always emits in the tx — the outbox ON CONFLICT
// absorbs the duplicate event_key). This is what makes
// DeletionReconciler safe to re-call every reconciliationInterval.
func TestEnqueueDriveDelete_SkipsAlreadyInFlightRow(t *testing.T) {
	for _, preExisting := range []string{"DELETE_REQUESTED", "DELETE_PENDING", "DRIVE_DELETE_PENDING", "INDEX_DELETE_PENDING", "DELETED"} {
		t.Run(preExisting, func(t *testing.T) {
			db := memoryDB(t)
			ensureOutboxSchema(t, db)
			minimalMediaAssetsFixture(t, db)
			originalUpdatedAt := "2024-01-01T00:00:00Z"
			insertRow(t, db, "asset_inflight", preExisting, originalUpdatedAt)

			d := NewDispatcher(&fakeClips{}, &fakeClips{}, outboxevents.NewRepository(db), &txMgrCapture{db: db}, zap.NewNop())
			if err := d.EnqueueDriveDelete(context.Background(), "asset_inflight", false); err != nil {
				t.Fatalf("EnqueueDriveDelete on pre-existing %s row: %v", preExisting, err)
			}

			var state, updatedAt string
			if err := db.QueryRow(`SELECT lifecycle_state, updated_at FROM media_assets WHERE id = ?`, "asset_inflight").Scan(&state, &updatedAt); err != nil {
				t.Fatalf("read row: %v", err)
			}
			if state != preExisting {
				t.Errorf("lifecycle_state was changed: want %s got %s (the WHERE NOT IN excludes this state)", preExisting, state)
			}
			if updatedAt != originalUpdatedAt {
				t.Errorf("updated_at was bumped: want %q got %q (skip path must not bump)", originalUpdatedAt, updatedAt)
			}
		})
	}
}

// TestEnqueueDriveDelete_StampsRowInActiveStateBumps confirms the
// inverse: an ACTIVE row IS bumped + flipped by the dispatcher.
// (Pairs with the SKIPS test above.)
func TestEnqueueDriveDelete_StampsRowInActiveStateBumps(t *testing.T) {
	db := memoryDB(t)
	ensureOutboxSchema(t, db)
	minimalMediaAssetsFixture(t, db)
	originalUpdatedAt := "2024-01-01T00:00:00Z"
	insertRow(t, db, "asset_active", "ACTIVE", originalUpdatedAt)

	d := NewDispatcher(&fakeClips{}, &fakeClips{}, outboxevents.NewRepository(db), &txMgrCapture{db: db}, zap.NewNop())
	if err := d.EnqueueDriveDelete(context.Background(), "asset_active", false); err != nil {
		t.Fatalf("EnqueueDriveDelete: %v", err)
	}

	var state, updatedAt string
	if err := db.QueryRow(`SELECT lifecycle_state, updated_at FROM media_assets WHERE id = ?`, "asset_active").Scan(&state, &updatedAt); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if state != "DELETE_REQUESTED" {
		t.Errorf("lifecycle_state: want DELETE_REQUESTED got %q", state)
	}
	if updatedAt == originalUpdatedAt {
		t.Errorf("updated_at: not bumped (got %q == pre-call %q) — DBR fix not applied", updatedAt, originalUpdatedAt)
	}
}

// ── Fail-fast guards (mirror delete_envelope_test.go's structure) ──

// TestEnqueueDriveDelete_NilPointerRejected.
func TestEnqueueDriveDelete_NilPointerRejected(t *testing.T) {
	var d *Dispatcher
	if err := d.EnqueueDriveDelete(context.Background(), "x", false); err == nil {
		t.Fatal("nil *Dispatcher must return error before any field access")
	}
}

// TestEnqueueDriveDelete_NilTxMgrRejected.
func TestEnqueueDriveDelete_NilTxMgrRejected(t *testing.T) {
	d := &Dispatcher{clips: &fakeClips{}, stateWriter: &fakeClips{}, outboxEventsRepo: nil}
	if err := d.EnqueueDriveDelete(context.Background(), "x", false); err == nil {
		t.Fatal("nil txmgr must return error before tx is opened")
	}
}

// TestEnqueueDriveDelete_NilOutboxEventsRejected.
func TestEnqueueDriveDelete_NilOutboxEventsRejected(t *testing.T) {
	d := &Dispatcher{clips: &fakeClips{}, stateWriter: &fakeClips{}, txmgr: txMgrNoop{}, outboxEventsRepo: nil}
	if err := d.EnqueueDriveDelete(context.Background(), "x", false); err == nil {
		t.Fatal("nil outboxEventsRepo must return error before tx is opened")
	}
}

// TestEnqueueDriveDelete_EmptyAssetIDRejected.
func TestEnqueueDriveDelete_EmptyAssetIDRejected(t *testing.T) {
	d := NewDispatcher(&fakeClips{}, &fakeClips{}, nil, txMgrNoop{}, zap.NewNop())
	if err := d.EnqueueDriveDelete(context.Background(), "", false); err == nil {
		t.Fatal("empty assetID must return error before tx is opened")
	}
}

// TestEnqueueAndDelete_ShimIsTrashRoute pins EnqueueAndDelete as
// the BACKWARD-COMPATIBILITY SHIM that routes to permanently=false
// (Trash route). Test confirms a single EnqueueAndDelete call lands
// an outbox row with event_key=`drive_delete:false:` — operators
// reading the outbox log can distinguish "shim caller" from
// "explicit Trash request" only by the event_key prefix, so the
// shim MUST route through the Trash path (per dispatcher_delete.go
// docstring contract).
func TestEnqueueAndDelete_ShimIsTrashRoute(t *testing.T) {
	db := memoryDB(t)
	ensureOutboxSchema(t, db)
	minimalMediaAssetsFixture(t, db)
	insertRow(t, db, "asset_shim", "ACTIVE", "2000-01-01T00:00:00Z")

	d := NewDispatcher(&fakeClips{}, &fakeClips{}, outboxevents.NewRepository(db), &txMgrCapture{db: db}, zap.NewNop())
	if err := d.EnqueueAndDelete(context.Background(), "asset_shim"); err != nil {
		t.Fatalf("EnqueueAndDelete: %v", err)
	}

	var eventKey string
	if err := db.QueryRow(`SELECT event_key FROM outbox_events ORDER BY id DESC LIMIT 1`).Scan(&eventKey); err != nil {
		t.Fatalf("scan event_key: %v", err)
	}
	if eventKey != "drive_delete:false:asset_shim" {
		t.Errorf("EnqueueAndDelete shim event_key: want drive_delete:false:asset_shim got %q", eventKey)
	}
}

// ────────────────────────────────────────────────────────────────────
// EnqueueIndexDelete CIRCUIT-BREAKER contract tests (Blocco 3.2
// commit 2/2 follow-up pin, July 2026).
//
// EnqueueIndexDelete is the canonical re-emit path for stuck
// mid-chain rows. Per the docstring on dispatcher_delete.go, it
// re-emits EventAssetIndexDeleteRequested WITHOUT advancing
// lifecycle_state (emit-only semantic) and re-stamps updated_at
// (CIRCUIT-BREAKER rate-limit on retries; see dispatcher_advance.go
// for the state-flip sibling).
//
// These tests pin both halves of the invariant. Without them:
//   - a future refactor that drops the updated_at re-stamp would let
//     the reconciler re-emit every reconciliationInterval (hot-loop
//     on permanent failures);
//   - a future refactor that lifts the emit-only guarantee would let
//     the reconciler chain the state forward out of order, racing
//     with the IndexDeleteHandler's at-handler state-flip.
// ────────────────────────────────────────────────────────────────────

// TestEnqueueIndexDelete_StampsUpdatedAtWithoutStateFlip pins the
// core CIRCUIT-BREAKER invariant for a row in DRIVE_DELETE_PENDING
// (a mid-chain state the reconciler would re-emit to recover from
// a stuck worker). Asserts:
//
//	(a) lifecycle_state is NOT changed (emit-only semantic).
//	(b) updated_at IS re-stamped to current time (CIRCUIT-BREAKER
//	    rate-limit; the next reconciler tick re-fires only after
//	    `threshold` minutes, not on every reconciliationInterval).
//	(c) outbox row emits EventAssetIndexDeleteRequested with
//	    event_key=`delete:<assetID>` + canonical v1 envelope shape.
//
// Pairs with TestEnqueueDriveDelete_StampsUpdatedAtOnLifecycleFlip
// from Blocco 3.2 commit 1/2 (the CIRCUIT-BREAKER pin on the
// state-flip path) — together they seal both halves of the blocco.
func TestEnqueueIndexDelete_StampsUpdatedAtWithoutStateFlip(t *testing.T) {
	db := memoryDB(t)
	ensureOutboxSchema(t, db)
	minimalMediaAssetsFixture(t, db)

	// Seed row in DRIVE_DELETE_PENDING — a mid-chain state where the
	// DeletionReconciler would re-emit to recover from a stuck worker
	// (e.g. Drive API permanently failed and the row dropped out of
	// the happy path but the worker didn't advance to index-delete).
	const preExistingState = "DRIVE_DELETE_PENDING"
	const assetID = "asset_midchain"
	const originalUpdatedAt = "2020-01-01T00:00:00Z"
	insertRow(t, db, assetID, preExistingState, originalUpdatedAt)

	beforeCall := time.Now().UTC()
	d := NewDispatcher(&fakeClips{}, &fakeClips{}, outboxevents.NewRepository(db), &txMgrCapture{db: db}, zap.NewNop())
	if err := d.EnqueueIndexDelete(context.Background(), assetID); err != nil {
		t.Fatalf("EnqueueIndexDelete: %v", err)
	}
	afterCall := time.Now().UTC()

	// (a) lifecycle_state preserved (CIRCUIT-BREAKER is emit-only).
	var state string
	var updatedAt string
	if err := db.QueryRow(`SELECT lifecycle_state, updated_at FROM media_assets WHERE id = ?`, assetID).Scan(&state, &updatedAt); err != nil {
		t.Fatalf("read media_assets row: %v", err)
	}
	if state != preExistingState {
		t.Errorf("lifecycle_state: CIRCUIT-BREAKER is emit-only; want %s unchanged got %s", preExistingState, state)
	}

	// (b) updated_at was re-stamped (between beforeCall and afterCall).
	got := timeutil.ParseRFC3339(updatedAt)
	if got.IsZero() {
		t.Fatalf("parse updated_at %q returned zero time", updatedAt)
	}
	if !got.After(beforeCall.Add(-time.Second)) || got.After(afterCall.Add(time.Second)) {
		t.Errorf("updated_at %s not within +/-1s of call window [%s, %s]", got, beforeCall, afterCall)
	}
	if updatedAt == originalUpdatedAt {
		t.Errorf("updated_at was NOT re-stamped (still %q) -- CIRCUIT-BREAKER fix not applied", updatedAt)
	}

	// (c) outbox row shape.
	var eventType, aggID, aggType, payload, eventKey string
	if err := db.QueryRow(`SELECT event_type, aggregate_id, aggregate_type, payload_json, event_key FROM outbox_events ORDER BY id DESC LIMIT 1`).Scan(&eventType, &aggID, &aggType, &payload, &eventKey); err != nil {
		t.Fatalf("scan outbox row: %v", err)
	}
	if eventType != outboxevents.EventAssetIndexDeleteRequested {
		t.Errorf("event_type: want %q got %q", outboxevents.EventAssetIndexDeleteRequested, eventType)
	}
	if aggID != assetID {
		t.Errorf("aggregate_id: want %q got %q", assetID, aggID)
	}
	if aggType != "media_asset" {
		t.Errorf("aggregate_type: want media_asset got %q", aggType)
	}
	if eventKey != "delete:"+assetID {
		t.Errorf("event_key: want delete:%s got %q", assetID, eventKey)
	}

	// v1 envelope (light shape check; round-trip is canonical).
	var p deleteRequestV1
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		t.Fatalf("json.Unmarshal v1 envelope: %v\npayload: %s", err, payload)
	}
	if p.SchemaVersion != DeleteRequestSchemaVersion {
		t.Errorf("schema_version: want %q got %q", DeleteRequestSchemaVersion, p.SchemaVersion)
	}
	if p.AssetID != assetID {
		t.Errorf("asset_id: want %q got %q", assetID, p.AssetID)
	}
	if p.EventID == "" {
		t.Error("event_id must be non-empty (operator audit UUID)")
	}
	if p.IdempotencyKey != eventKey {
		t.Errorf("v1 conflation invariant: payload.IdempotencyKey (%q) != event_key (%q)", p.IdempotencyKey, eventKey)
	}
	if p.RequestedAt == "" {
		t.Error("requested_at: must be non-empty RFC3339 (operator audit)")
	}
}

// TestEnqueueIndexDelete_PreservesLifecycleStateAcrossAllMidChainStates
// is the boundary pin for the emit-only semantic across every
// deletion-chain state. Catches a hypothetical future refactor that
// accidentally wires EnqueueIndexDelete to dispatcher_advance.go's
// state-flip SQL (which would silently advance the row's lifecycle_state
// even when only re-emission was intended).
//
// For each pre-existing state, asserts the same 3 invariants:
//
//	(i)   lifecycle_state is unchanged (no accidental state advance).
//	(ii)  updated_at is re-stamped within +/-1s of call time
//	      (CIRCUIT-BREAKER rate-limit).
//	(iii) outbox row emits with event_key=`delete:<assetID>`.
//
// Subtest-driven so a future regression pinpoints the offending state
// directly (the failing state's name surfaces in `go test -v`).
func TestEnqueueIndexDelete_PreservesLifecycleStateAcrossAllMidChainStates(t *testing.T) {
	for _, preExisting := range []string{
		"ACTIVE",               // non-chain: classifies to emit-only fallback (rare canonical path)
		"DELETE_REQUESTED",     // mid-chain hop 1 (would advance->DRIVE_DELETE_PENDING if swapped)
		"DRIVE_DELETE_PENDING", // mid-chain hop 2 (canonical reconciler target)
		"INDEX_DELETE_PENDING", // mid-chain hop 3 (would advance->DELETED if swapped)
		"DELETED",              // terminal (rare path; should still tolerate)
	} {
		t.Run(preExisting, func(t *testing.T) {
			db := memoryDB(t)
			ensureOutboxSchema(t, db)
			minimalMediaAssetsFixture(t, db)
			const originalUpdatedAt = "2024-01-01T00:00:00Z"
			insertRow(t, db, "asset_inflight", preExisting, originalUpdatedAt)

			beforeCall := time.Now().UTC()
			d := NewDispatcher(&fakeClips{}, &fakeClips{}, outboxevents.NewRepository(db), &txMgrCapture{db: db}, zap.NewNop())
			if err := d.EnqueueIndexDelete(context.Background(), "asset_inflight"); err != nil {
				t.Fatalf("EnqueueIndexDelete on pre-existing %s row: %v", preExisting, err)
			}
			afterCall := time.Now().UTC()

			// (i) lifecycle_state preserved.
			var state, updatedAt string
			if err := db.QueryRow(`SELECT lifecycle_state, updated_at FROM media_assets WHERE id = ?`, "asset_inflight").Scan(&state, &updatedAt); err != nil {
				t.Fatalf("read row: %v", err)
			}
			if state != preExisting {
				t.Errorf("lifecycle_state changed: want %s got %s (CIRCUIT-BREAKER is emit-only -- if swapped with dispatcher_advance.go, chain would advance out of order)", preExisting, state)
			}

			// (ii) updated_at re-stamped within +/-1s.
			got := timeutil.ParseRFC3339(updatedAt)
			if got.IsZero() {
				t.Errorf("updated_at %q is not parseable as RFC3339", updatedAt)
			}
			if !got.After(beforeCall.Add(-time.Second)) || got.After(afterCall.Add(time.Second)) {
				t.Errorf("updated_at %s not within +/-1s of call window [%s, %s]", got, beforeCall, afterCall)
			}
			if updatedAt == originalUpdatedAt {
				t.Errorf("updated_at was NOT re-stamped (still %q) -- CIRCUIT-BREAKER fix not applied", updatedAt)
			}

			// (iii) outbox row emitted.
			var eventKey string
			if err := db.QueryRow(`SELECT event_key FROM outbox_events ORDER BY id DESC LIMIT 1`).Scan(&eventKey); err != nil {
				t.Fatalf("scan event_key: %v", err)
			}
			if eventKey != "delete:asset_inflight" {
				t.Errorf("event_key: want delete:asset_inflight got %q", eventKey)
			}
		})
	}
}
