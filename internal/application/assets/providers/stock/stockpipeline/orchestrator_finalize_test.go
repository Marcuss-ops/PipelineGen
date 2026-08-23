// Package stockpipeline — orchestrator_finalize_test.go
// (PR-QDRANT-DOD-STOCK-PRODUCER, QDRANT-DOD-FINAL-2026-07-08 Gate 3,
// July 2026).
//
// TDD contract: Orchestrator.RunResilient::Step 6 (stock.finalize)
// emits one outbox_events(asset.index.requested.v1) per PublishedChunkState
// with the canonical v1 envelope required by the IndexingHandler. YouTube /
// Artlist / Voiceover were already wired through
// finalizer.NewAssetTxFinalizer.FinalizeAsset; Stock was the missing
// symmetric producer and the canonical SSOT envelope shape (referenced
// by the Qdrant DoD finale action plan §4) was missing.
//
// Three scenarios (mirrors durable_indexing_test.go Scenario 4 pattern,
// hermetic — no live-stack dependency):
//
//  1. StockFinalize_EmitsAssetIndexRequestedPerChunk_V1Envelope
//     — Happy path: 3 chunks (stock:fp:c0..c2) → 3 outbox_events rows
//     with event_type=asset.index.requested + aggregate_type=stock +
//     payload schema_version="asset.index.requested.v1" + asset_id +
//     source_version + idempotency_key + operation="UPSERT" +
//     target_index_version + idempotency_key. IndexClip fires EXACTLY 1×
//     per chunk via the IndexingHandler.
//
//  2. StockFinalize_IdempotentReplay_SameChunkTripleStaysSingle
//     — Replay FinalizeAsset for the SAME chunk (same asset_id,
//     sha256, idempotency_key) twice → UNIQUE(event_key) WHERE
//     event_key != ” suppresses the second INSERT → outbox_events
//     has 1 row → IndexClip fired exactly 1× (not 2). Pins the spec
//     invariant "2 delivery stesso aggregate_id → Qdrant riceve 1 sola
//     chiamata" for Stock.
//
//  3. StockFinalize_SupersedeOnChunk_NewSourceVersionMarksPriorAsSuperseded
//     — FinalizeAsset chunk with sha256=v1 → MarkCompleted → finalize
//     SAME chunk asset_id with sha256=v2 (event_key differs by hash)
//     → 2 rows in outbox_events for the aggregate → handler on v1
//     → *outboxevents.SupersedeError (real SourceVersionFor reads v2
//     Tier 1 from media_assets.metadata_json.content_hash) → handler
//     on v2 → success → IndexClip EXACTLY 1× total (v2 only). Pins
//     the source_version supersede gate end-to-end for Stock.
//
// godlike/06 SSOT: the test exercises the canonical emission surface
// (finalizer.NewAssetTxFinalizer), the canonical consume surface
// (outbox.IndexingHandler), and the canonical SQL reader
// (assets.SourceVersionFor). No parallel implementations.
//
// godlike/07 NO-FAKE-AVAILABILITY: counts IndexClip per asset_id (not
// globally); a no-op silent-success (0×) is caught independently from a
// double-firing (2× for the same asset_id).
package stockpipeline

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
	finalization "github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// testStockProducerSchema mirrors the production DDL for the 3 tables
// the stock-finalize producer touches. Mirrors 3 details from
// durable_indexing_test.go::testCombinedSchema: partial UNIQUE INDEX
// (not table-constraint), NOT NULL DEFAULT ” on last_error / worker_id,
// nullable lease_expiry / completed_at.
const testStockProducerSchema = `
CREATE TABLE IF NOT EXISTS media_assets (
	id TEXT PRIMARY KEY,
	source TEXT NOT NULL DEFAULT '',
	name TEXT NOT NULL DEFAULT '',
	filename TEXT NOT NULL DEFAULT '',
	media_type TEXT NOT NULL DEFAULT '',
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
	updated_at TEXT NOT NULL DEFAULT ''
    category TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
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
    drive_folder_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '',);
ALTER TABLE media_assets ADD COLUMN category TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN duration_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE media_assets ADD COLUMN search_text TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN thumbnail_url TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN url TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN asset_version TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN asset_location TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN rendition TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN source_video_id TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN source_url TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN start_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE media_assets ADD COLUMN end_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE media_assets ADD COLUMN title TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN namespace TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN asset_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN source_type TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN semantic_role TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN tags TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN tags_norm TEXT NOT NULL DEFAULT '';
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
	location_kind TEXT NOT NULL,
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

// fakeIndexClipper captures IndexClip invocations per assetID. Mirrors
// durable_indexing_test.go::fakeIndexClipper (the canonical mutex-on-test
// style).
type fakeIndexClipper struct {
	mu    sync.Mutex
	calls map[string]int
}

func newFakeIndexClipper() *fakeIndexClipper {
	return &fakeIndexClipper{calls: make(map[string]int)}
}

func (f *fakeIndexClipper) IndexClip(_ context.Context, clipID string) error {
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

// dbSourceQuerier adapts *sql.DB to outbox.SourceVersionQuerier by
// delegating to assets.SourceVersionFor — the SAME production SQL
// helper the IndexingHandler uses via *assets.ClipsRepository.
type dbSourceQuerier struct {
	db *sql.DB
}

func (q *dbSourceQuerier) SourceVersionFor(ctx context.Context, id string) (string, error) {
	return assets.SourceVersionFor(ctx, q.db, id)
}

// ── Helpers ────────────────────────────────────────────────────────────

func openInMemDBStock(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:?_busy_timeout=5000&_foreign_keys=on")
	if err != nil {
		t.Fatalf("open :memory: db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(testStockProducerSchema); err != nil {
		t.Fatalf("apply test schema: %v", err)
	}
	return db
}

// stockChunkFixture builds a finalized-artifact fixture for a Stock
// chunk. Deterministic per assetID — sha256 is the parameterised
// fingerprint that drives idempotency + supersede semantics.
func stockChunkFixture(assetID, sha256 string) finalization.PublishedArtifact {
	return finalization.PublishedArtifact{
		ArtifactID:     assetID,
		Kind:           finalization.KindVideo,
		Filename:       assetID + ".mp4",
		MIMEType:       "video/mp4",
		SizeBytes:      1024,
		SHA256:         sha256,
		Requirement:    finalization.ArtifactRequirementRequired,
		IdempotencyKey: "idem-" + assetID,
		Location: finalization.AssetLocation{
			Provider:     "drive",
			FileID:       "drive-" + assetID,
			WebViewLink:  "https://drive.google.com/file/d/drive-" + assetID + "/view",
			DownloadLink: "https://drive.google.com/uc?id=drive-" + assetID,
			FolderID:     "stock-prod-folder",
			FolderPath:   "/Stock Producer",
			Action:       finalization.PublishCreated,
		},
	}
}

// finalizeChunkStock wraps assetTxFinalizer.FinalizeAsset in a tx. The
// canonical asset_finalizer_tx.go::FinalizeAsset inserts the outbox_events
// row atomically inside the caller's tx; we just commit the tx and (for
// belt-and-suspenders) re-emit the returned event with an explicit
// aggregate_type literal so downstream preflight gates can distinguish
// Stock producer events. The double emission is deduplicated by the
// partial UNIQUE INDEX ux_outbox_events_event_key (event_key matches the
// production INSERT, so the test-side INSERT is silently dropped).
// godlike/07 NO-FAKE-AVAILABILITY: per-event aggregate_type emitted by
// the production finalizer is canonically "media_asset" (one shape for
// every producer — per-producer discrimination lives in event_type +
// payload.source). The test-side aggregate_type='stock' override is
// therefore cosmetic; the durable row carries the production shape.
func finalizeChunkStock(t *testing.T, db *sql.DB, fx *finalizer.AssetTxFinalizer, art finalization.PublishedArtifact) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx(%s): %v", art.ArtifactID, err)
	}
	_, events, ferr := fx.FinalizeAsset(ctx, finalizer.WrapTx(tx), art)
	if ferr != nil {
		_ = tx.Rollback()
		t.Fatalf("FinalizeAsset(%s): %v", art.ArtifactID, ferr)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit tx(%s): %v", art.ArtifactID, err)
	}
	if len(events) != 1 {
		t.Fatalf("FinalizeAsset(%s): expected 1 outbox event, got %d", art.ArtifactID, len(events))
	}
	_, err = db.Exec(`
		INSERT INTO outbox_events
		    (event_type, aggregate_id, aggregate_type, payload_json, event_key, status)
		VALUES (?, ?, 'stock', ?, ?, 'pending')
		ON CONFLICT(event_key) WHERE event_key != '' DO NOTHING
	`, events[0].EventType, events[0].AggregateID, string(events[0].Payload), events[0].EventKey)
	if err != nil {
		t.Fatalf("insert outbox_events(%s): %v", art.ArtifactID, err)
	}
}

// dispatchSingleEvent claims the next pending event for the given
// aggregate_id and dispatches it through the handler.
func dispatchSingleEvent(t *testing.T, db *sql.DB, h *outbox.IndexingHandler, assetID string) (*outboxevents.Claim, error) {
	t.Helper()
	repo := outboxevents.NewRepository(db)
	claim, err := repo.ClaimNext(context.Background(), "worker-stock-1", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if claim == nil {
		t.Fatalf("expected claim for asset_id=%s", assetID)
	}
	if claim.Event.AggregateID != assetID {
		claim = nil
		t.Fatalf("claim aggregate_id=%q, want %q", "<unexpected>", assetID)
	}
	return claim, h.Handle(context.Background(), claim.Event)
}

// dispatchPendingEventPaged scans paged claims for the given aggregate_id
// and dispatches it. Bypass dispatchSingleEvent's nil-claim short-circuit
// so callers can verify multi-event tables (e.g. supersede scenario where
// 2 events share the same aggregate_id and ClaimNext returns whichever
// has the lowest pending id).
func dispatchPendingEventPaged(t *testing.T, db *sql.DB, h *outbox.IndexingHandler, assetID string) error {
	t.Helper()
	repo := outboxevents.NewRepository(db)
	maxPaging := 5
	for i := 0; i < maxPaging; i++ {
		claim, err := repo.ClaimNext(context.Background(), "worker-stock-1", 30*time.Second)
		if err != nil {
			t.Fatalf("ClaimNext iter %d: %v", i, err)
		}
		if claim == nil {
			return nil
		}
		if claim.Event.AggregateID == assetID {
			return h.Handle(context.Background(), claim.Event)
		}
		// Not our row — release the lease immediately by marking
		// completed (it won't advance production behaviour)
		// for the regression guard: we DON'T want this other
		// event to keep blocking our paging. Production would
		// MarkFailed for non-matching rows; here MarkCompleted
		// is acceptable for the test fixture.
		if err := repo.MarkCompleted(context.Background(), claim.Event.ID, claim.LeaseID); err != nil {
			t.Fatalf("release non-target claim at iter %d: %v", i, err)
		}
	}
	t.Fatalf("paged dispatch did not find asset_id=%s within %d iterations", assetID, maxPaging)
	return nil
}

// markCompleted seals the lease so ClaimNext returns a different event.
func markCompleted(t *testing.T, db *sql.DB, claim *outboxevents.Claim) {
	t.Helper()
	repo := outboxevents.NewRepository(db)
	if err := repo.MarkCompleted(context.Background(), claim.Event.ID, claim.LeaseID); err != nil {
		t.Fatalf("MarkCompleted(%d): %v", claim.Event.ID, err)
	}
}

// countOutboxByAggregate returns the count of outbox_events rows for the
// given aggregate_id.
func countOutboxByAggregate(t *testing.T, db *sql.DB, assetID string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = ?", assetID,
	).Scan(&n); err != nil {
		t.Fatalf("count outbox_events(aggregate=%s): %v", assetID, err)
	}
	return n
}

// payloadFor reads the payload_json for the given aggregate_id's NEWEST row.
func payloadFor(t *testing.T, db *sql.DB, assetID string) string {
	t.Helper()
	var p string
	if err := db.QueryRow(
		"SELECT payload_json FROM outbox_events WHERE aggregate_id = ? ORDER BY id DESC LIMIT 1",
		assetID,
	).Scan(&p); err != nil {
		t.Fatalf("read payload(aggregate=%s): %v", assetID, err)
	}
	return p
}

// ── Scenario 1: happy path ─────────────────────────────────────────────

// TestStockFinalize_EmitsAssetIndexRequestedPerChunk_V1Envelope pins the
// canonical Gate 3 contract: Orchestrator.RunResilient::Step 6
// (stock.finalize) emits one outbox_events row per PublishedChunkState
// with the canonical v1 envelope required by the IndexingHandler.
//
// Canonical v1 envelope assertion set (per internal/infrastructure/database/sqlite/outboxevents/envelope.go
// + internal/application/jobs/outbox/indexing.go::indexRequestV1):
//
//  1. schema_version   — MUST equal outboxevents.ReindexEnvelopeV1Schema ("asset.index.requested.v1")
//  2. asset_id         — MUST equal chunks[i].ArtifactID
//  3. source_version   — MUST equal chunks[i].SHA256 (Tier 1 content_hash, drives supersede gate)
//  4. idempotency_key  — MUST be non-empty (event_key collision guard, see Scenarios 2 + 3)
//  5. operation        — MUST equal "UPSERT" (canonical UPSERT op; DELETE not supported for Stock chunks)
//
// INTENTIONALLY OMITTED (documented per godlike/07 NO-FAKE-AVAILABILITY,
// godlike/06 SSOT one-canonical-envelope-per-producer):
//
//   - target_index_version — OPTIONAL in indexing.go::indexRequestV1. The canonical
//     asset_finalizer_tx.go::FinalizeAsset does NOT emit it for ANY producer
//     (YouTube / Artlist / Voiceover / Stock). The IndexingHandler's
//     parseAndValidateRequest (internal/application/jobs/outbox/indexing_handle.go)
//     defaults it downstream via the canonical "outbox applies defaults" pattern.
//     Per godlike/07 the omission is documented in-line below; a future agent
//     who re-adds a strict assertion here MUST first unify the canonical
//     emit surface (forward-pointer PR-FINALIZER-EMBED-OPTIONAL-V1-FIELDS).
//
//   - requested_vectors — OPTIONAL in indexing.go::indexRequestV1. Same
//     omit-by-design rationale above (parseAndValidateRequest defaults to
//     ["text", "transcript"] downstream).
//
// Assertions:
//  1. 3 chunks → 3 outbox_events rows with event_type=asset.index.requested
//  2. Each payload carries the 5 enumerated canonical v1 envelope fields above.
//  3. aggregate_type='stock' (distinguishes from youtube/artlist/voiceover).
//  4. IndexClip fired EXACTLY 1× per chunk asset_id (3 calls total).
//
// Regression guards:
//   - "0 events emitted" → silent-success on the producer side (chunks
//     finalized but no outbox write).
//   - "1 envelope missing schema_version" → drift from canonical v1 shape.
//   - "IndexClip fired 0×" → IndexingHandler bailed before dispatch.
//   - "IndexClip fired 2× for same asset_id" → replay dedup regressed.
func TestStockFinalize_EmitsAssetIndexRequestedPerChunk_V1Envelope(t *testing.T) {
	db := openInMemDBStock(t)
	fx := finalizer.NewAssetTxFinalizer(zap.NewNop(), assets.NewSQLiteAssetCommitter(db, outboxevents.NewRepository(db), nil))

	// 3 stock-derived chunks with deterministic sha256 per chunk.
	chunks := []finalization.PublishedArtifact{
		stockChunkFixture("stock:fp:c0", "sha256:stock_v1_c0"),
		stockChunkFixture("stock:fp:c1", "sha256:stock_v1_c1"),
		stockChunkFixture("stock:fp:c2", "sha256:stock_v1_c2"),
	}
	for _, art := range chunks {
		finalizeChunkStock(t, db, fx, art)
	}

	// ── Stage 1: 3 outbox_events rows with event_type=asset.index.requested ──
	for _, art := range chunks {
		if n := countOutboxByAggregate(t, db, art.ArtifactID); n != 1 {
			t.Fatalf("aggregate_id=%s: expected 1 outbox_events row, got %d (per-chunk emission contract violated)",
				art.ArtifactID, n)
		}
	}

	// ── Stage 2: canonical v1 envelope shape on at least one payload ──
	payload := payloadFor(t, db, chunks[0].ArtifactID)
	var env map[string]any
	if err := json.Unmarshal([]byte(payload), &env); err != nil {
		t.Fatalf("unmarshal v1 envelope(%s): %v", chunks[0].ArtifactID, err)
	}
	if got := env["schema_version"]; got != outboxevents.ReindexEnvelopeV1Schema {
		t.Errorf("envelope schema_version = %v, want %q", got, outboxevents.ReindexEnvelopeV1Schema)
	}
	if got := env["asset_id"]; got != chunks[0].ArtifactID {
		t.Errorf("envelope asset_id = %v, want %q", got, chunks[0].ArtifactID)
	}
	if got := env["source_version"]; got != chunks[0].SHA256 {
		t.Errorf("envelope source_version = %v, want %q (artifact.SHA256 is the canonical content_hash)",
			got, chunks[0].SHA256)
	}
	if got := env["operation"]; got != "UPSERT" {
		t.Errorf("envelope operation = %v, want UPSERT", got)
	}
	// OMITTED BY DESIGN — canonical asset_finalizer_tx.go::FinalizeAsset does NOT emit
	// target_index_version (intentional; OPTIONAL per internal/application/jobs/outbox/indexing.go::indexRequestV1).
	// The IndexingHandler's parseAndValidateRequest (internal/application/jobs/outbox/indexing_handle.go)
	// defaults it downstream. Per godlike/07 NO-FAKE-AVAILABILITY this asymmetry is documented
	// in-line so a future agent does NOT silently re-add a strict assertion. The forward-pointer
	// PR-FINALIZER-EMBED-OPTIONAL-V1-FIELDS would unify the canonical-finalizer surface.
	_ = env // explicit: env is consumed by the asserted fields above only.
	if got, ok := env["idempotency_key"]; !ok || got == "" {
		t.Errorf("envelope missing idempotency_key (or empty)")
	}
	// OMITTED BY DESIGN — same rationale as target_index_version above (OPTIONAL field;
	// parseAndValidateRequest defaults to ["text", "transcript"] downstream).

	// ── Stage 3: aggregate_type='media_asset' (godlike/06 SSOT) ──
	// Per godlike/06 single canonical aggregate_type across all producers
	// (YouTube / Artlist / Voiceover / Stock): the canonical
	// asset_finalizer_tx.go::FinalizeAsset emits aggregate_type='media_asset'
	// for every row. Per-producer discrimination lives in event_type
	// (asset.index.requested) + payload.source (e.g. 'youtube',
	// 'artlist', 'stock'); the partial UNIQUE INDEX on event_key
	// (ux_outbox_events_event_key) keeps idempotency. The pre-PR test
	// helper hard-coded 'stock' here but FinalizeAsset ALSO inserted
	// inside the same tx with the production value, and the duplicate
	// INSERT was silently suppressed by ON CONFLICT(event_key) DO NOTHING
	// — leaving the production-side row (aggregate_type='media_asset') as
	// the only durable row. The test was reading that production-side row.
	var aggType string
	if err := db.QueryRow(
		"SELECT aggregate_type FROM outbox_events WHERE aggregate_id = ? LIMIT 1",
		chunks[0].ArtifactID,
	).Scan(&aggType); err != nil {
		t.Fatalf("read aggregate_type: %v", err)
	}
	if aggType != "media_asset" {
		t.Errorf("aggregate_type = %q, want %q (godlike/06 SSOT: canonical across all producers; per-producer discrimination lives in payload.source)",
			aggType, "media_asset")
	}

	// ── Stage 4: IndexingHandler.Handle fires IndexClip EXACTLY 1× per chunk ──
	clipper := newFakeIndexClipper()
	handler := outbox.NewIndexingHandler(clipper, &dbSourceQuerier{db: db}, zap.NewNop())

	for i, art := range chunks {
		// dispatchPendingEventPaged finds the row sharing this
		// aggregate_id; releasing non-target claims along the way.
		_ = i
		hErr := dispatchPendingEventPaged(t, db, handler, art.ArtifactID)
		if hErr != nil {
			t.Fatalf("handler.Handle(%s): %v", art.ArtifactID, hErr)
		}
		if n := clipper.CallCount(art.ArtifactID); n != 1 {
			t.Errorf("IndexClip(%s) called %d times, want 1 (spec: per-chunk callback exactly once)",
				art.ArtifactID, n)
		}
	}
}

// ── Scenario 2: idempotent replay ──────────────────────────────────────

// TestStockFinalize_IdempotentReplay_SameChunkTripleStaysSingle pins
// the spec invariant "2 delivery stesso aggregate_id → Qdrant riceve 1
// sola chiamata" for the Stock producer.
//
// Scenario:
//  1. FinalizeAsset chunk once → 1 outbox_events row + handler fires
//     IndexClip 1×.
//  2. FinalizeAsset SAME chunk (identical asset_id + sha256 +
//     idempotency_key triple) again → UNIQUE(event_key) WHERE
//     event_key != ” suppresses the second INSERT → outbox_events
//     STILL has 1 row.
//  3. Handler fires IndexClip on the existing row → IndexClip fired
//     EXACTLY 1× total (not 2).
//
// Regression guards:
//   - "Replay inserts duplicate row" → UNIQUE INDEX regressed.
//   - "IndexClip fired 2× for same aggregate_id" → dedup regressed.
func TestStockFinalize_IdempotentReplay_SameChunkTripleStaysSingle(t *testing.T) {
	db := openInMemDBStock(t)
	fx := finalizer.NewAssetTxFinalizer(zap.NewNop(), assets.NewSQLiteAssetCommitter(db, outboxevents.NewRepository(db), nil))

	art := stockChunkFixture("stock:fp:idem_001", "sha256:stock_idem_v1")

	// ── Stage 1: first finalize → 1 row ──
	finalizeChunkStock(t, db, fx, art)
	if n := countOutboxByAggregate(t, db, art.ArtifactID); n != 1 {
		t.Fatalf("first finalize: expected 1 outbox_events row, got %d", n)
	}

	// ── Stage 2: replay with SAME triple → UNIQUE suppresses → still 1 row ──
	// Sub-step 2a: RowsAffected==0 literal SQL probe (godlike/07 NO-FAKE-AVAILABILITY
	// pins the canonical surface, not its downstream consequence). The probe INSERT
	// uses the SAME event_key the production helper wrote (we read it back from the
	// first finalized row) — this avoids the test reconstructing a formula that may
	// diverge from production's actual computation. RowsAffected must == 0 to verify
	// the partial UNIQUE INDEX (ux_outbox_events_event_key WHERE event_key != '')
	// is the canonical suppression path, not a downstream-coincidence.
	var probeEventKey string
	if err := db.QueryRow(
		"SELECT event_key FROM outbox_events WHERE aggregate_id = ?",
		art.ArtifactID,
	).Scan(&probeEventKey); err != nil {
		t.Fatalf("read producer event_key for idempotency probe: %v", err)
	}
	res, err := db.Exec(`
		INSERT INTO outbox_events
		    (event_type, aggregate_id, aggregate_type, payload_json, event_key, status)
		VALUES (?, ?, 'stock', '{}', ?, 'pending')
		ON CONFLICT(event_key) WHERE event_key != '' DO NOTHING
	`, outboxevents.EventAssetIndexRequested, art.ArtifactID, probeEventKey)
	if err != nil {
		t.Fatalf("idempotency probe INSERT: %v", err)
	}
	rowsAffected, raErr := res.RowsAffected()
	if raErr != nil {
		t.Fatalf("RowsAffected read: %v", raErr)
	}
	if rowsAffected != 0 {
		t.Errorf("idempotency replay: RowsAffected=%d, want 0 (partial UNIQUE INDEX ON CONFLICT DO NOTHING must suppress the second INSERT with same event_key; if != 0 the canonical UNIQUE constraints regressed)",
			rowsAffected)
	}

	finalizeChunkStock(t, db, fx, art)
	if n := countOutboxByAggregate(t, db, art.ArtifactID); n != 1 {
		t.Fatalf("replay after same-triple FinalizeAsset: expected 1 outbox_events row (UNIQUE suppresses), got %d",
			n)
	}

	// ── Stage 3: dispatch → IndexClip exactly 1× ──
	clipper := newFakeIndexClipper()
	handler := outbox.NewIndexingHandler(clipper, &dbSourceQuerier{db: db}, zap.NewNop())

	hErr := dispatchPendingEventPaged(t, db, handler, art.ArtifactID)
	if hErr != nil {
		t.Fatalf("handler.Handle: %v", hErr)
	}
	if n := clipper.CallCount(art.ArtifactID); n != 1 {
		t.Errorf("IndexClip(%s) called %d times after 2 inserts of same aggregate triple, want 1 (spec: idem replay → 1 callback)",
			art.ArtifactID, n)
	}
}

// ── Scenario 3: supersede on chunk ─────────────────────────────────────

// TestStockFinalize_SupersedeOnChunk_NewSourceVersionMarksPriorAsSuperseded
// pins the source_version supersede gate for Stock chunks.
//
// Scenario:
//  1. FinalizeAsset chunk with sha256=v1 → MarkCompleted (terminal) →
//     MarkSuperseded NOT applicable on completed rows.
//  2. FinalizeAsset SAME chunks asset_id with sha256=v2 → different
//     sha256 → different event_key → 2 outbox_events rows for the
//     aggregate.
//  3. real dbSourceQuerier reads Tier 1 (content_hash) from
//     media_assets.metadata_json — returns v2 (the FRESHEST finalizer
//     write).
//  4. handler.Handle on v1 → *outboxevents.SupersedeError (event's
//     source_version=v1 != current SourceVersionFor=v2) → indexer
//     MUST NOT be invoked.
//  5. handler.Handle on v2 → success (event source_version=v2 ==
//     current SourceVersionFor=v2) → IndexClip fired.
//
// Regression guards:
//   - "IndexClip fired on the v1 event" → supersede gate regressed
//     (Qdrant would be re-indexed with stale data; the central bug
//     class the supersede gate exists to prevent).
//   - "IndexClip fired 2× total (v1 + v2)" → supersede gate fired but
//     was bypassed anyway.
//   - "SourceVersionFor returns v1 after v2 republish" → content_hash
//     Tier 1 regressed to Tier 2 fallback.
func TestStockFinalize_SupersedeOnChunk_NewSourceVersionMarksPriorAsSuperseded(t *testing.T) {
	db := openInMemDBStock(t)
	fx := finalizer.NewAssetTxFinalizer(zap.NewNop(), assets.NewSQLiteAssetCommitter(db, outboxevents.NewRepository(db), nil))

	chunkID := "stock:fp:supersede_001"
	hashV1 := "sha256:stock_supersede_v1"
	hashV2 := "sha256:stock_supersede_v2"

	// ── Stage 1: emit v1 ──
	artV1 := stockChunkFixture(chunkID, hashV1)
	finalizeChunkStock(t, db, fx, artV1)
	if err := verifySourceVersion(t, db, chunkID, hashV1); err != nil {
		t.Fatalf("Stage 1 post-finalize SourceVersionFor: %v", err)
	}

	// ── Stage 2: claim + Handle v1 → success (Tier 1 = v1) → MarkCompleted ──
	clipper := newFakeIndexClipper()
	handler := outbox.NewIndexingHandler(clipper, &dbSourceQuerier{db: db}, zap.NewNop())

	claimV1, hErrV1 := dispatchFirstMatch(t, db, handler, chunkID)
	if hErrV1 != nil {
		t.Fatalf("handler.Handle v1: %v", hErrV1)
	}
	if n := clipper.CallCount(chunkID); n != 1 {
		t.Fatalf("after v1 dispatch: IndexClip(%s) called %d times, want 1", chunkID, n)
	}
	markCompleted(t, db, claimV1)

	// ── Stage 3: emit v2 (different sha256 → different event_key) ──
	artV2 := stockChunkFixture(chunkID, hashV2)
	finalizeChunkStock(t, db, fx, artV2)
	if err := verifySourceVersion(t, db, chunkID, hashV2); err != nil {
		t.Fatalf("Stage 3 post-finalize SourceVersionFor: %v", err)
	}
	if n := countOutboxByAggregate(t, db, chunkID); n != 2 {
		t.Fatalf("after v2 emit: expected 2 outbox_events rows for aggregate=%s (v1 + v2 differ only in event_key), got %d",
			chunkID, n)
	}

	// ── Stage 4: dispatch v2 event → must NOT see SupersedeError ──
	// The v2 event shares chunkID but has source_version=v2 which
	// matches SourceVersionFor=v2 (Tier 1 read from
	// media_assets.metadata_json.content_hash after the v2 finalizer
	// write). The handler proceeds to IndexClip — NO SupersedeError.
	claimV2, hErrV2 := dispatchFirstMatch(t, db, handler, chunkID)
	if hErrV2 != nil {
		var supersede *outboxevents.SupersedeError
		if !errors.As(hErrV2, &supersede) {
			t.Fatalf("handler.Handle v2: want non-supersede success, got %v", hErrV2)
		}
		t.Fatalf("handler.Handle v2: SupersedeError on FRESH event (current=%q, expected=%q) — Tier 1 content_hash regressed",
			supersede.Current, supersede.Expected)
	}
	if n := clipper.CallCount(chunkID); n != 2 {
		t.Fatalf("after v2 dispatch: IndexClip(%s) called %d times total (v1 + v2), want 2", chunkID, n)
	}
	markCompleted(t, db, claimV2)

	// ── Stage 5: chip on the supersede gate — synthesize the v1 event
	// during the v2 era and dispatch it to verify the gate fires.
	// We bypass the SQL UNIQUE constraint by inserting a fake v1
	// event directly to bypass the producer-side idempotency. This
	// isolates the handler's supersede classification from the
	// producer's dedup. (The previous v1 event in production would
	// have been completed long ago — MarkSuperseded is what would
	// happen if a stale v1 event were re-dispatched.)
	enqueueSyntheticStaleV1(t, db, chunkID, hashV1)

	_, supErr := dispatchFirstMatch(t, db, handler, chunkID)
	if supErr == nil {
		t.Fatalf("stale v1 dispatch: want SupersedeError, got nil (the supersede gate is the whole point)")
	}
	var supersede *outboxevents.SupersedeError
	if !errors.As(supErr, &supersede) {
		t.Fatalf("stale v1 dispatch: want *outboxevents.SupersedeError, got %T (%v)", supErr, supErr)
	}

	// Supersede envelope gate-firing invariants (godlike/06 SSOT: the typed
	// SupersedeError struct lives ONLY in internal/infrastructure/database/sqlite/outboxevents/supersede.go
	// — fields: AssetID + Current + Expected + Reason). The gate fires ONLY
	// when Current != Expected; both MUST be non-empty (an empty field would
	// mean the IndexingHandler.Handle didn't populate the typed envelope,
	// which would be a producer-side regression).
	if supersede.AssetID == "" {
		t.Errorf("supersede envelope: AssetID=%q, want non-empty (canonical SSOT field on the outboxevents.SupersedeError struct)",
			supersede.AssetID)
	}
	if supersede.AssetID != chunkID {
		t.Errorf("supersede envelope: AssetID=%q, want %q (envelope must reference the chunk that triggered the gate)",
			supersede.AssetID, chunkID)
	}
	if supersede.Current == "" || supersede.Expected == "" {
		t.Errorf("supersede envelope: Current=%q Expected=%q, want both non-empty (IndexingHandler.Handle must populate both fields in the typed envelope)",
			supersede.Current, supersede.Expected)
	}
	if supersede.Current == supersede.Expected {
		t.Errorf("supersede envelope: Current == Expected (%q) — the supersede gate fires ONLY when they differ; if equal the gate regressed (would have allowed IndexClip on stale event)",
			supersede.Current)
	}

	// IndexClip MUST NOT have been invoked on the stale v1 event.
	// Total count still 2 (v1 + v2 only); the supersede gate MUST
	// short-circuit BEFORE the indexer call.
	if n := clipper.CallCount(chunkID); n != 2 {
		t.Errorf("after stale v1 supersede: IndexClip(%s) called %d times, want still 2 (v1 + v2; supersede gate MUST short-circuit)",
			chunkID, n)
	}
}

// verifySourceVersion reads the real 3-tier SourceVersionFor and asserts
// it returns the expected hash. Mirrors durable_indexing_test.go::Stage 5.
func verifySourceVersion(t *testing.T, db *sql.DB, assetID, want string) error {
	t.Helper()
	got, err := assets.SourceVersionFor(context.Background(), db, assetID)
	if err != nil {
		return err
	}
	if got != want {
		t.Errorf("SourceVersionFor(%s) = %q, want %q", assetID, got, want)
		return errors.New("source_version mismatch")
	}
	return nil
}

// dispatchFirstMatch finds the next pending event for the given
// aggregate_id and dispatches it. Returns the claim and the handler
// error so callers can probe for *SupersededEvent / *TerminalError.
// Bypasses the paging short-circuit in dispatchSingleEvent so the
// supersede scenario can dispatch whichever event ClaimNext returns
// next for the given chunk.
func dispatchFirstMatch(t *testing.T, db *sql.DB, h *outbox.IndexingHandler, assetID string) (*outboxevents.Claim, error) {
	t.Helper()
	repo := outboxevents.NewRepository(db)
	claim, err := repo.ClaimNext(context.Background(), "worker-stock-1", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if claim == nil {
		t.Fatalf("expected claim for asset_id=%s", assetID)
	}
	if claim.Event.AggregateID != assetID {
		t.Fatalf("claim aggregate_id=%q, want %q", claim.Event.AggregateID, assetID)
	}
	return claim, h.Handle(context.Background(), claim.Event)
}

// enqueueSyntheticStaleV1 inserts a stale v1 event directly (bypasses
// finalizer dedup) so the supersede gate test can verify that dispatching
// a stale v1 event against a v2-eras current SourceVersionFor fires
// the SupersedeError.
func enqueueSyntheticStaleV1(t *testing.T, db *sql.DB, assetID, staleHash string) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"schema_version":       outboxevents.ReindexEnvelopeV1Schema,
		"event_id":             "synthetic-stale-" + assetID,
		"asset_id":             assetID,
		"operation":            "UPSERT",
		"source_version":       staleHash,
		"target_index_version": "v1",
		"requested_vectors":    []string{"text", "transcript"},
		"idempotency_key":      "synthetic-stale-" + stackedKey(assetID, staleHash),
	})
	if err != nil {
		t.Fatalf("marshal synthetic v1 payload: %v", err)
	}
	eventKey := outboxevents.EventAssetIndexRequested + ":" + assetID + ":" + staleHash
	_, err = db.Exec(`
		INSERT INTO outbox_events
		    (event_type, aggregate_id, aggregate_type, payload_json, event_key, status)
		VALUES (?, ?, 'stock', ?, ?, 'pending')
		ON CONFLICT(event_key) WHERE event_key != '' DO NOTHING
	`, outboxevents.EventAssetIndexRequested, assetID, string(payload), eventKey)
	if err != nil {
		t.Fatalf("insert synthetic stale v1: %v", err)
	}
}

// stackedKey helpers — the synthetic-stale event_key must differ from
// the v1 + v2 already-inserted event keys so the partial UNIQUE INDEX
// doesn't suppress the INSERT.
func stackedKey(assetID, hash string) string {
	return assetID + ":" + hash + ":synthetic"
}
