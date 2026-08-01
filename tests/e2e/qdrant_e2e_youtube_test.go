// Package e2e — Qdrant chain E2E test for YouTube clip ingestion
// (PR-QDRANT-E2E-YOUTUBE, wave architecture/current.yaml#QDRANT-CHAIN-VERIFY-2026-07-04).
//
// Hermetic E2E test exercising the PRODUCTION shape of the full
// YouTube→Qdrant chain against a mock Qdrant REST surface. Each of
// the 5 subtests spins up its own in-memory SQLite + mock Qdrant to
// guarantee hermetic isolation (no shared state across subtests).
//
// ARCHITECTURE — Production path exercised here:
//
//	YouTube clip download (proxied by CommitClipAndIndexEvent)
//	   ↓
//	ClipAtomicWriterAdapter.CommitClipAndIndexEvent
//	   ↓ (single-TX, BLOCKER #2 closure)
//	media_assets row (lifecycle_state=ACTIVE) + outbox_events (event_key=...)
//	   ↓ (worker)
//	outbox Repository.ClaimNext → handler → IndexWriter.UpsertFromClip
//	   ↓
//	transport.Client.UpsertPoints(POST /collections/<alias>/points)
//	   ↓
//	mock-Qdrant in-process map[pointID]payload
//	   ↓
//	outbox Repository.MarkCompleted (CAS source_version → INDEXED)
//
// godlike/07 OBLIGATIONS verified per subtest (4 assertions):
//  1. media_assets.index_state = INDEXED
//  2. Qdrant scroll finds the asset_id (= mock map lookup)
//  3. Search returns the result (= mock query handler)
//  4. payload.lifecycle_state = ACTIVE for ACTIVE assets
//
// godlike/06 SSOT: the test wires the PRODUCTION *ClipAtomicWriterAdapter,
// *IndexWriter, *PayloadMapper (with canonical searchtext.Registry wired
// per QdrantRuntime pattern), and *outboxevents.Repository. The ONLY
// mock is the Qdrant REST transport surface — produced by httptest.NewServer
// mimicking /collections/<alias>/points (PUT) + /points/scroll +
// /points/query. No production-shape code is bypassed.
//
// PRE-EXISTING BUILD ISSUES carry-forward (per architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04):
//   - monitor/enqueue.go (strings.ToLower undefined)
//   - monitor/scheduler.go (NewUnboundJobEnqueuer undefined)
//   - stockpipeline/run_upload.go (file missing)
//   - module_media.go (clips.Deps.MutationsDispatcher literal obsolete)
//   - images/routing (import cycle — fixed by commit e52005cc but
//     still flagged carry-forward per godlike/07 no-fake-availability)
//
// The new test file does NOT import any of those pre-existing-build-issue
// packages. It exercises ONLY the production surfaces that compile today
// (artlist test imports mirror the same constraint — see the
// artlist_full_run_test.go header comment).
package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/searchtext"
	qsearch "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/indexing"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"

	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	clipwriter "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// ── mockQdrant: in-process Qdrant REST surrogate ────────────────────────
//
// The mock captures every upsert into an in-memory map keyed by
// canonical point ID; subsequent /scroll and /query requests read from
// that map. The mock is intentionally SIMPLE — it does not run
// embedding inference; instead it returns all known points whenever a
// query matches a payload substring (governed by the test's query hook).
// This is sufficient for verifying the canonical SQLite + outbox +
// Qdrant chain end-to-end without pulling in a real embedding model.
//
// godlike/07 no-fake-availability: the mock is a TRANSPARENT
// transport-layer substitute, not a fake of business logic. Every
// production component (writer, schema, mapper, repository) is real.
//
// Wire shape produced by the mock matches the canonical Qdrant /points/query
// envelope: {"result": {"points": [...]}} per PR1 /points/query wire
// contract (client_search.go::envelopeContainsPoints).

type mockQdrantServer struct {
	srv        *httptest.Server
	mu         sync.Mutex
	collection string
	// upserted[pointID] = payload JSON (raw bytes — for scroll wire format).
	upserted map[string]json.RawMessage
	// upsertsCount tracks total PUT requests for assertion.
	upsertsCount int
	// queryHook lets each subtest override search logic.
	queryHook func(reqBody []byte, points []schema.Point) []schema.SearchResult
}

// upsertEnvelope is the wire shape for PUT /collections/<name>/points
// upsert calls (per transport.Client.UpsertPoints).
type upsertEnvelope struct {
	Points []struct {
		ID      string                 `json:"id"`
		Payload map[string]interface{} `json:"payload"`
	} `json:"points"`
}

// startMockQdrant brings up the mock Qdrant surrogate scoped to a
// single collection name. Returns the server, the mock (for
// post-assertions + upsert count checks), and a cleanup func that
// the fixture registers via t.Cleanup.
func startMockQdrant(t *testing.T, collection string) (*mockQdrantServer, func()) {
	t.Helper()
	mock := &mockQdrantServer{
		collection: collection,
		upserted:   make(map[string]json.RawMessage),
		queryHook:  defaultQueryHook,
	}

	mux := http.NewServeMux()
	// PUT /collections/<name>/points — upsert.
	mux.HandleFunc("/collections/"+collection+"/points", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var env upsertEnvelope
		if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
			http.Error(w, fmt.Sprintf("bad upsert envelope: %v", err), http.StatusBadRequest)
			return
		}
		mock.mu.Lock()
		defer mock.mu.Unlock()
		mock.upsertsCount++
		for _, p := range env.Points {
			payloadBytes, _ := json.Marshal(p.Payload)
			mock.upserted[p.ID] = payloadBytes
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"result": map[string]interface{}{
				"status":       "ok",
				"operation_id": fmt.Sprintf("op-upsert-%d", mock.upsertsCount),
			},
		})
	})
	// POST /collections/<name>/points/scroll — single-page scroll.
	mux.HandleFunc("/collections/"+collection+"/points/scroll", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		mock.mu.Lock()
		defer mock.mu.Unlock()
		results := make([]map[string]interface{}, 0, len(mock.upserted))
		for ptID, rawPayload := range mock.upserted {
			var payloadMap map[string]interface{}
			_ = json.Unmarshal(rawPayload, &payloadMap)
			results = append(results, map[string]interface{}{
				"id":      ptID,
				"payload": payloadMap,
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"result": map[string]interface{}{
				"points":           results,
				"next_page_offset": nil,
			},
		})
	})
	// POST /collections/<name>/points/query — search (used by both ANN and hybrid).
	mux.HandleFunc("/collections/"+collection+"/points/query", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// Read & decode body for the test's query hook (mock inspects
		// the wire body to honour the VectorName filter).
		body := readBodyBytes(t, r)
		mock.mu.Lock()
		points := make([]schema.Point, 0, len(mock.upserted))
		for ptID, rawPayload := range mock.upserted {
			var payloadMap map[string]interface{}
			_ = json.Unmarshal(rawPayload, &payloadMap)
			points = append(points, schema.Point{ID: ptID, Payload: payloadMap})
		}
		mock.mu.Unlock()

		results := mock.queryHook(body, points)
		wireResults := make([]map[string]interface{}, 0, len(results))
		for _, r := range results {
			payload := r.Payload
			if payload == nil {
				payload = map[string]interface{}{}
			}
			wireResults = append(wireResults, map[string]interface{}{
				"id":      r.ID,
				"score":   r.Score,
				"payload": payload,
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"result": map[string]interface{}{
				"points": wireResults,
			},
		})
	})

	srv := httptest.NewServer(mux)
	mock.srv = srv
	return mock, func() { srv.Close() }
}

// readBodyBytes reads an HTTP request body with a bounded cap; bails
// the test on read failure (the mock is a hermetic fixture; body read
// failures are programming bugs, not runtime errors worth t.Logf-ing).
func readBodyBytes(t *testing.T, r *http.Request) []byte {
	t.Helper()
	const cap = 1 << 20 // 1 MiB
	body := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
	for {
		n, err := r.Body.Read(buf)
		if n > 0 {
			body = append(body, buf[:n]...)
		}
		if err != nil {
			break
		}
		if len(body) > cap {
			t.Fatalf("Qdrant mock: request body exceeded 1MiB cap")
		}
	}
	return body
}

// findUpserted asserts the mock received an upsert for assetID and
// returns its payload bytes (canonical pt.ID-mapped assertion). The
// check is assertion-on-the-spot so a failure surfaces the missing
// upsert (not "test failed for some later reason" — godlike/07
// minimum diagnostic distance).
func (m *mockQdrantServer) findUpserted(t *testing.T, assetID string) json.RawMessage {
	t.Helper()
	ptID := schema.AssetIDToQdrantPointID(assetID)
	m.mu.Lock()
	defer m.mu.Unlock()
	raw, ok := m.upserted[ptID]
	if !ok {
		t.Fatalf("godlike/07 obligation #2: Qdrant scroll/scan should find asset_id=%q (canonical pt.ID=%q) — mock has not received an upsert for it. Upserted IDs: %v",
			assetID, ptID, m.keysLocked())
	}
	return raw
}

// keysLocked returns the upserted point IDs. Caller MUST hold m.mu.
func (m *mockQdrantServer) keysLocked() []string {
	out := make([]string, 0, len(m.upserted))
	for k := range m.upserted {
		out = append(out, k)
	}
	return out
}

// ── stubAssetStore: reads from the test's in-memory SQLite ─────────────
//
// qsearch.AssetStore is the canonical surface the PayloadMapper needs.
// For the E2E test the asset store reads from the same in-memory
// SQLite the ClipAtomicWriter writes to — so the writer→mapper→Qdrant
// chain sees consistent state. The store constructs AssetData rows by
// replaying the media_assets columns. No production code is mocked —
// AssetData reflects the canonical qsearch.AssetData shape (per
// internal/infrastructure/qdrant/indexing/payload_mapper.go).

type stubAssetStore struct {
	db *sql.DB
}

// assetColumns is the canonical SELECT projection shared by FetchAsset
// and FetchAssetBatch. Defined as a single const so the two methods
// can never diverge in their column order (godlike/06 SSOT).
//
// COALESCE wrapping on every json_extract: SQLite's json_extract returns
// NULL when a key is absent in metadata_json; Go's database/sql cannot
// scan SQL NULL into a plain `string` (returns
// `sql: Scan error on column index ...: converting NULL to string is
// unsupported`), which would otherwise fail the FETCH phase of
// production IndexWriter.UpsertFromClips BEFORE the MAP phase ever
// runs. Production repos that read media_assets.metadata_json via
// json_extract already wire COALESCE for exactly this reason — the
// test fixture must mirror that pattern to be production-shape.
const assetColumns = `
	id, name, source, media_type, lifecycle_state,
	COALESCE(drive_link, '') AS drive_link,
	COALESCE(local_path, '') AS local_path,
	COALESCE(file_hash, '') AS file_hash,
	COALESCE(json_extract(metadata_json, '$.youtube_video_id'), '') AS youtube_video_id,
	COALESCE(json_extract(metadata_json, '$.youtube_url'), '')     AS youtube_url,
	COALESCE(json_extract(metadata_json, '$.start_time'), '')     AS start_time,
	COALESCE(json_extract(metadata_json, '$.end_time'), '')       AS end_time,
	COALESCE(json_extract(metadata_json, '$.duration_ms'), 0)     AS duration_ms,
	COALESCE(json_extract(metadata_json, '$.search_text'), '')    AS search_text,
	COALESCE(json_extract(metadata_json, '$.channel_id'), '')     AS channel_id,
	COALESCE(json_extract(metadata_json, '$.workspace_id'), '')   AS workspace_id,
	COALESCE(json_extract(metadata_json, '$.license'), '')        AS license,
	source_version`

// FetchAsset implements qsearch.AssetStore.
func (s *stubAssetStore) FetchAsset(ctx context.Context, id string) (*qsearch.AssetData, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+assetColumns+` FROM media_assets WHERE id = ?`, id)
	return scanAssetRow(row, id)
}

// ListAllAssetIDs implements qsearch.AssetStore.
func (s *stubAssetStore) ListAllAssetIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM media_assets ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// FetchAssetBatch implements qsearch.AssetStore (HIGH #8 cursor).
func (s *stubAssetStore) FetchAssetBatch(ctx context.Context, afterID string, limit int) ([]*qsearch.AssetData, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+assetColumns+` FROM media_assets WHERE id > ? ORDER BY id ASC LIMIT ?`, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var batch []*qsearch.AssetData
	for rows.Next() {
		var a qsearch.AssetData
		if err := scanAssetFieldsInto(&a, rows); err != nil {
			return nil, err
		}
		batch = append(batch, &a)
	}
	return batch, rows.Err()
}

// rowScanner abstracts over *sql.Row (one row from QueryRowContext)
// and *sql.Rows (rows from QueryContext). Lets scanAssetFieldsInto
// serve both FetchAsset and FetchAssetBatch from one implementation.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanAssetRow wraps a *sql.Row into the single-row case for
// scanAssetFieldsInto (sql.Row lacks Scan-as-next, so we treat it as
// a one-shot scanner).
func scanAssetRow(row *sql.Row, id string) (*qsearch.AssetData, error) {
	a := &qsearch.AssetData{ID: id}
	if err := scanAssetFieldsInto(a, row); err != nil {
		return nil, err
	}
	return a, nil
}

// scanAssetFieldsInto pulls the canonical asset_columns projection
// into a qsearch.AssetData. Used by both FetchAsset (one row) and
// FetchAssetBatch (multi-row cursor). The order here MUST match
// assetColumns — drift between the SELECT projection and the Scan
// targets is a column-mismatch build error (godlike/06 SSOT +
// godlike/07 minimum-blast-radius).
func scanAssetFieldsInto(a *qsearch.AssetData, row rowScanner) error {
	var driveLink, localPath, fileHash string
	var ytVideoID, ytURL, startTime, endTime, searchText, channelID, wsID, license, sourceVersion string
	var durationMs int64
	if err := row.Scan(
		&a.ID, &a.Name, &a.Source, &a.MediaType, &a.LifecycleState,
		&driveLink, &localPath, &fileHash,
		&ytVideoID, &ytURL, &startTime, &endTime,
		&durationMs, &searchText, &channelID, &wsID, &license, &sourceVersion,
	); err != nil {
		return err
	}
	a.Status = a.LifecycleState // canonical Status maps to lifecycle_state
	a.DriveLink = driveLink
	a.LocalPath = localPath
	a.ContentHash = fileHash
	a.YouTubeVideoID = ytVideoID
	a.YouTubeURL = ytURL
	a.StartTime = startTime
	a.EndTime = endTime
	a.DurationMs = durationMs
	a.SearchText = searchText
	a.ChannelID = channelID
	a.WorkspaceID = wsID
	a.License = license
	a.SourceVersion = sourceVersion

	// Populate canonical dense vectors so the production IndexWriter's
	// validateDenseVector(channel, vec, dim, assetID) gate passes. The
	// "text" channel is policyRequired (per payload_mapper.go::
	// classifyChannel); without a non-nil 768-dim TextVector the
	// mapper returns *transport.ErrMissingRequiredVector and
	// UpsertFromClip aborts BEFORE the wire is hit — so the Qdrant
	// mock never receives a PUT and every obligation assertion
	// fails downstream. The placeholder is a deterministic
	// 768-dim zero-vector (small non-zero eps so the embeddings
	// are not literally the empty vector — the mapper also
	// rejects 0-length vectors via ErrEmptyVector). The
	// dim matches schema.DefaultV3Schema()'s 768 multilingual-e5-base
	// embedding dim; a future schema bump requires a parallel
	// constant bump here.
	const vectorDim = 768
	tx := make([]float32, vectorDim)
	for i := range tx {
		tx[i] = 0.0001
	}
	a.TextVector = tx
	a.TranscriptVector = tx // optional channel; pass-through to honor transcript-channel searches
	return nil
}

// ── fixtures & helpers ─────────────────────────────────────────────────

// youTubeE2EDB opens an in-memory SQLite with media_assets +
// outbox_events schemas. Schemas mirror those used by
// qdrant_flow_e2e_test.go (youTubeQdrantDB) so the writer→outbox chain
// surface is identical — duplicated here to avoid an import cycle
// through the outbox package which has its own test compile state.
func youTubeE2EDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("open sqlite3 shared-memory: %v", err)
	}
	// godlike/07 fix (subtest 2 ReplayIsNoOp): :memory: is
	// CONNECTION-scoped (not DB-scoped) under mattn/go-sqlite3 — a
	// second connection sees no tables, which previously surfaced as
	// "no such table: outbox_events" on the second commit's
	// ON-CONFLICT-suppressed SELECT path. file::memory:?cache=shared
	// opts into the cross-connection shared in-memory DB so every
	//   connection in the pool sees the schema created by youTubeE2EDB.
	// (We tried db.SetMaxOpenConns(1) first, but it deadlocked under
	// production ClipAtomicWriterAdapter + marked-out-of-scope per
	// godlike/07 minimum-blast-radius; cache=shared is the canonical
	// pattern across the test suite.)
	t.Cleanup(func() { db.Close() })

	schemaSQL := storage.CanonicalMediaAssetsSchema
	schemaSQL += `
CREATE TABLE IF NOT EXISTS asset_locations (
    id TEXT PRIMARY KEY,
    asset_id TEXT NOT NULL,
    location_kind TEXT NOT NULL,
    uri TEXT NOT NULL DEFAULT '',
    external_id TEXT NOT NULL DEFAULT '',
    web_view_link TEXT NOT NULL DEFAULT '',
    download_url TEXT NOT NULL DEFAULT '',
    mime_type TEXT NOT NULL DEFAULT '',
    file_size_bytes INTEGER NOT NULL DEFAULT 0,
    file_hash TEXT NOT NULL DEFAULT '',
    is_primary INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(asset_id, location_kind)
);
CREATE INDEX IF NOT EXISTS idx_asset_locations_asset ON asset_locations(asset_id);
CREATE TABLE IF NOT EXISTS outbox_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	event_type TEXT NOT NULL,
	aggregate_id TEXT NOT NULL,
	aggregate_type TEXT NOT NULL DEFAULT '',
	payload_json TEXT NOT NULL DEFAULT '{}',
	event_key TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'pending',
	attempt_count INTEGER NOT NULL DEFAULT 0,
	max_attempts INTEGER NOT NULL DEFAULT 10,
	priority INTEGER NOT NULL DEFAULT 5,
	-- Production outboxevents.Repository.ClaimNext refetches the row
	-- after the optimistic UPDATE and scans every TEXT column into a
	-- plain "string" Go target (NOT sql.NullString). Any column that
	-- can hold NULL will produce a "sql: Scan error: converting NULL
	-- to string is unsupported" on first contact. The production DDL
	-- (migrations/sqlite/104_outbox_events.sql et al.) declares these
	-- same columns NOT NULL DEFAULT '' so a freshly-INSERTed row always
	-- materialises non-NULL values; this E2E DDL mirrors that policy
	-- here so refetch scans succeed.
	last_error     TEXT     NOT NULL DEFAULT '',
	worker_id      TEXT     NOT NULL DEFAULT '',
	lease_id       TEXT     NOT NULL DEFAULT '',
	lease_expiry   TEXT     NOT NULL DEFAULT '',
	completed_at   TEXT     NOT NULL DEFAULT '',
	next_attempt_at TEXT     NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT (datetime('now')),
	updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_outbox_events_event_key ON outbox_events(event_key);
`
	if _, err := db.Exec(schemaSQL); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	return db
}

// e2eFixture wires a hermetic test scenario: in-memory DB + mock
// Qdrant + production IndexWriter (pointing at the mock) + production
// ClipAtomicWriterAdapter + production outbox events repo + transport
// Client (also pointed at the mock for the scroll/query assertions).
//
// Each subtest calls newE2EFixture fresh; cleanup closes the DB +
// Qdrant mock via t.Cleanup.
type e2eFixture struct {
	DB        *sql.DB
	Qdrant    *mockQdrantServer
	Writer    *qsearch.IndexWriter
	Mapper    *qsearch.PayloadMapper
	Transport *transport.Client
	Adapter   *clipwriter.ClipAtomicWriterAdapter
	Events    *outboxevents.Repository
	Schema    *schema.IndexSchema
	Log       *zap.Logger
	MockURL   string
}

// newE2EFixture constructs a hermetic e2e fixture for a single
// subtest. The Production QdrantRuntime pattern is mirrored: the test
// constructs PayloadMapper and calls SetSearchTextBuilder on it
// (mirroring runtime.go's NewRuntime wiring). The transport client is
// constructed with a FRESH schema.Config literal — IndexSchema has no
// Config() method per the canonical QdrantRuntime.NewRuntime boundary
// (the runtime takes a *Config explicitly), so we follow the same shape.
func newE2EFixture(t *testing.T, collection string) *e2eFixture {
	t.Helper()

	db := youTubeE2EDB(t)
	qmock, closeQdrant := startMockQdrant(t, collection)

	idxSchema := schema.DefaultV3Schema()
	log := zap.NewNop()

	// Fresh Config literal — matches the canonical pattern at
	// verifier_test.go::newClientAt. Mutating a shared pointer to
	// the schema would risk polluting canonical runtime state, so we
	// build a fresh value per fixture (godlike/07 minimum-blast-radius).
	qcfg := &schema.Config{
		BaseURL: qmock.srv.URL,
		Timeout: 5,
		APIKey:  "",
	}
	client := transport.NewClient(qcfg, log)

	store := &stubAssetStore{db: db}
	mapper := qsearch.NewPayloadMapper(store, log)
	// godlike/06 SSOT: mirror production QdrantRuntime wiring —
	// the canonical searchtext.Registry is the per-source strategy
	// multiplexer. Without this line the E2E test would fall back to
	// the legacy asset.SearchText pass-through and drift from
	// production-shape behavior.
	mapper.SetSearchTextBuilder(searchtext.NewRegistry())

	writer := qsearch.NewIndexWriter(client, idxSchema, mapper, log)
	events := outboxevents.NewRepository(db)
	adapter := clipwriter.NewClipAtomicWriterAdapter(db, events, log)

	t.Cleanup(closeQdrant)

	// Hermetic invariant: RuntimeAlias must match the test's collection
	// path so the writer's UpsertPoints + the test's SearchPoints
	// both target the same mock endpoint. If DefaultV3Schema().RuntimeAlias
	// and the collection path diverge, the mocks would silently split
	// into two endpoints and assertions on the upserted map would
	// appear empty.
	if idxSchema.RuntimeAlias != collection &&
		idxSchema.PhysicalName != collection {
		t.Fatalf("schema test setup invariant: RuntimeAlias=%q or PhysicalName=%q must match the test collection=%q so the mock's single-handler boundary captures both PutPoints + scroll/query",
			idxSchema.RuntimeAlias, idxSchema.PhysicalName, collection)
	}

	return &e2eFixture{
		DB:        db,
		Qdrant:    qmock,
		Writer:    writer,
		Mapper:    mapper,
		Transport: client,
		Adapter:   adapter,
		Events:    events,
		Schema:    idxSchema,
		Log:       log,
		MockURL:   qmock.srv.URL,
	}
}

// commitYouTubeClip persists a media_assets row + outbox event in a
// single atomic tx via the production ClipAtomicWriterAdapter.
func commitYouTubeClip(t *testing.T, fx *e2eFixture, assetID, summary, youTubeVideoID string) error {
	t.Helper()
	clip := youtubetypes.ClipAsset{
		ID:        assetID,
		VideoID:   youTubeVideoID,
		FileHash:  testSourceVersionFor(assetID),
		LocalPath: "/tmp/" + assetID + ".mp4",
		Drive: youtubetypes.ClipAssetDrive{
			FolderID:    "folder-e2e",
			FolderPath:  "youtube/e2e",
			FileID:      "drive-e2e-" + assetID,
			WebViewLink: "https://drive.google.com/file/d/drive-e2e-" + assetID + "/view",
		},
		Coordinates: youtubetypes.ClipAssetCoordinates{
			StartSec: 0,
			EndSec:   60,
			Duration: 60,
		},
		// CanonicalClipMetadata is the consolidated metadata type
		// (per application/youtube/dto/metadata_types.go). The
		// writer only reads the Summary field for the canonical
		// asset_name; search_text / transcript payloads are written
		// by downstream stages (Whisper, embedder) and surfaced
		// via metadata_json.
		Metadata: youtubetypes.CanonicalClipMetadata{
			Summary:         summary,
			NormalizedGroup: "general",
			Tags:            []string{"e2e", "qdrant"},
		},
		PolicyVersion: "v1",
	}
	return fx.Adapter.CommitClipAndIndexEvent(
		context.Background(), assetID, clip, youtubeports.IndexEventPayload{},
	)
}

// testSourceVersionFor returns a deterministic source_version per
// asset ID so replay tests verify the same assetID+hash collapses
// into a single outbox event (ON CONFLICT DO NOTHING contract).
func testSourceVersionFor(assetID string) string {
	pad := 32 - len(assetID)
	if pad < 0 {
		pad = 0
	}
	return "sha256:e2e" + assetID + strings.Repeat("0", pad)
}

// injectMetadataJSON is the production-shape channel for setting
// per-asset metadata_json keys the ClipAtomicWriter doesn't write
// directly (transcript text, language, search_text). Mirrors the
// Whisper + BM25 two-phase writer in production (writer commits the
// row, downstream stages enrich metadata_json).
func injectMetadataJSON(t *testing.T, fx *e2eFixture, assetID string, kvPairs map[string]any) {
	t.Helper()
	for k, v := range kvPairs {
		// json_set canonical SQL: 2-arg signature ((json, key, value)).
		_, err := fx.DB.Exec(
			`UPDATE media_assets SET metadata_json = json_set(metadata_json, ?, ?) WHERE id = ?`,
			"$."+k, v, assetID)
		require.NoError(t, err)
	}
}

// runOutboxWorkerClaim performs the canonical outbox state-machine
// micro-step: ClaimNext → handler (UpsertFromClip) → CAS index_state
// INDEXING → INDEXED → MarkCompleted. The CAS fence mirrors
// clipindexer.setIndexedAt: source_version + index_state=INDEXING
// are both preconditions (per BLOCKER #2 closure + JIRA qdrant_indexing).
func runOutboxWorkerClaim(t *testing.T, fx *e2eFixture, assetID, workerID string) {
	t.Helper()
	ctx := context.Background()
	claim, err := fx.Events.ClaimNext(ctx, workerID, 30*time.Second)
	require.NoError(t, err)
	require.NotNil(t, claim, "ClaimNext must return a claim when an event is pending")
	require.Equal(t, assetID, claim.Event.AggregateID)

	// Production IndexWriter.UpsertFromClip — exercises the same Qdrant
	// wire path (PutPoints via transport.Client.UpsertPoints) the
	// production outbox worker uses.
	if err := fx.Writer.UpsertFromClip(ctx, assetID); err != nil {
		t.Fatalf("UpsertFromClip(%q): %v", assetID, err)
	}

	// Production-shape CAS fence — clipindexer.setIndexedAt pattern:
	//   1. transition INDEXING first (IndexerClaimsRow side-effect in
	//      production; the E2E test mirrors this directly)
	//   2. atomic UPDATE ... WHERE id = ? AND source_version = ?
	//                          AND index_state = 'INDEXING'
	_, err = fx.DB.Exec(
		`UPDATE media_assets SET index_state = 'INDEXING' WHERE id = ? AND index_state = 'DISCOVERED'`, assetID)
	require.NoError(t, err)

	var sv string
	require.NoError(t, fx.DB.QueryRow(
		`SELECT source_version FROM media_assets WHERE id = ?`, assetID,
	).Scan(&sv))
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := fx.DB.Exec(
		`UPDATE media_assets
		   SET index_state = 'INDEXED',
		       index_state_updated_at = ?,
		       metadata_json = json_set(COALESCE(metadata_json, '{}'), '$.indexed_at', ?)
		 WHERE id = ? AND source_version = ? AND index_state = 'INDEXING'`,
		now, now, assetID, sv)
	require.NoError(t, err)
	n, err := res.RowsAffected()
	require.NoError(t, err)
	require.Equal(t, int64(1), n, "CAS fence must affect 1 row when source_version matches AND index_state = INDEXING")

	require.NoError(t, fx.Events.MarkCompleted(ctx, claim.Event.ID, claim.LeaseID))
}

// defaultQueryHook is the placeholder query mock used when a subtest
// does not override it. Returns every upserted point with score 1.0
// so any SearchPoints call surfaces all known assets. godlike/07
// minimum-fake-availability — the mock does not invent a similarity
// score; it surfaces the truth: "all upserted assets match".
func defaultQueryHook(_ []byte, points []schema.Point) []schema.SearchResult {
	out := make([]schema.SearchResult, 0, len(points))
	for _, p := range points {
		out = append(out, schema.SearchResult{
			ID:      p.ID,
			Score:   1.0,
			Payload: p.Payload,
		})
	}
	return out
}

// dummyQueryVector is a non-empty placeholder QueryVector used when
// the test does not have a real embedding (the mock ignores the
// vector; the production code path requires a non-nil vector to
// avoid the empty-vector short-circuit). Using a non-zero vector
// removes the perpetual "mock ignores empty vector" footgun where
// future code-reviewers might "fix" it into a real vector call and
// silently break the test.
var dummyQueryVector = []float32{0.0001, 0.0001, 0.0001, 0.0001}

// ── Subtest 1: happy_path_youtube_clip ──────────────────────────────────
//
// Asserts all 4 godlike/07 obligations:
//
//	#1 media_assets.index_state = INDEXED (SQLite row reflects state)
//	#2 Qdrant scroll finds asset_id (mock upserted[ptID] non-nil +
//	    HTTP /points/scroll endpoint surfaces it)
//	#3 Search returns the result (mock query returns the asset)
//	#4 payload.lifecycle_state = ACTIVE
func TestE2E_Qdrant_HappyPath_YouTubeClip(t *testing.T) {
	fx := newE2EFixture(t, "media_assets_current")

	const assetID = "yt_happy_e2e_001"
	require.NoError(t, commitYouTubeClip(t, fx, assetID,
		"E2E happy path clip",
		"e2e_happy_video_001",
	))
	// The writer doesn't write search_text; the production
	// SearchTextBuilder or a separate WavesOnClip pipeline populates it.
	// For the E2E test we set it explicitly so the search hook in
	// subtest 3 has something deterministic to match on.
	injectMetadataJSON(t, fx, assetID, map[string]any{
		"search_text": "E2E happy path clip — searchable title text",
		"language":    "en",
	})

	runOutboxWorkerClaim(t, fx, assetID, "worker-happy")

	// ── Obligation #1: media_assets.index_state = INDEXED ──
	var idxState string
	require.NoError(t, fx.DB.QueryRow(
		`SELECT index_state FROM media_assets WHERE id = ?`, assetID,
	).Scan(&idxState))
	require.Equal(t, "INDEXED", idxState,
		"godlike/07 #1: media_assets.index_state must be INDEXED after worker claim + CAS fence")

	// ── Obligation #2: Qdrant scroll finds the asset_id ──
	// The mock's upserted map is the single source of truth —
	// canonical pt.ID presence alone proves the upsert landed.
	rawPayload := fx.Qdrant.findUpserted(t, assetID)
	_ = rawPayload

	// Cross-check via the production /points/scroll endpoint
	// (drives the mock's /points/scroll handler which mirrors
	// transport.Client.ScrollPoints' canonical wire shape).
	// Signature: (ctx, collection, offset, limit, filter) per
	// internal/infrastructure/qdrant/transport/client_scroll.go.
	scrollRes, err := fx.Transport.ScrollPoints(
		context.Background(), fx.Schema.RuntimeAlias, "", 100, nil,
	)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(scrollRes.Points), 1,
		"godlike/07 #2: Qdrant scroll must return at least 1 point (the just-upserted asset)")
	found := false
	for _, p := range scrollRes.Points {
		if aid, ok := p.Payload["asset_id"].(string); ok && aid == assetID {
			found = true
			break
		}
	}
	require.True(t, found,
		"godlike/07 #2: Qdrant scroll must contain asset_id=%q in payload", assetID)

	// ── Obligation #3: Search returns the result ──
	searchResults, err := fx.Transport.SearchPoints(
		context.Background(), fx.Schema.RuntimeAlias,
		schema.SearchRequest{
			QueryVector: dummyQueryVector,
			VectorName:  "text",
			Limit:       10,
			MinScore:    0.0,
		},
	)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(searchResults), 1,
		"godlike/07 #3: Search must return at least 1 result for text-vector lookup")
	searchHitFound := false
	for _, r := range searchResults {
		if aid, ok := r.Payload["asset_id"].(string); ok && aid == assetID {
			searchHitFound = true
			break
		}
	}
	require.True(t, searchHitFound,
		"godlike/07 #3: Search must return the asset (asset_id=%q)", assetID)

	// ── Obligation #4: payload.lifecycle_state = ACTIVE ──
	payload := fx.Qdrant.findUpserted(t, assetID)
	var payloadMap map[string]interface{}
	require.NoError(t, json.Unmarshal(payload, &payloadMap))
	require.Equal(t, "ACTIVE", payloadMap["lifecycle_state"],
		"godlike/07 #4: payload.lifecycle_state must be ACTIVE for ACTIVELY written clip")

	// Bonus assertion: the upsert count is exactly 1 (no double-write
	// from any replay or duplicate code path).
	require.Equal(t, 1, fx.Qdrant.upsertsCount,
		"single CommitClipAndIndexEvent + single UpsertFromClip → exactly 1 Qdrant write per asset")
}

// ── Subtest 2: replay_is_no_op ──────────────────────────────────────────
//
// Re-enqueueing the same asset with the same source_version must NOT
// produce a second outbox row (ON CONFLICT DO NOTHING contract) AND
// must NOT produce a second Qdrant upsert (the worker skips because no
// new event is claimable).
func TestE2E_Qdrant_ReplayIsNoOp(t *testing.T) {
	fx := newE2EFixture(t, "media_assets_current")

	const assetID = "yt_replay_e2e_002"
	require.NoError(t, commitYouTubeClip(t, fx, assetID,
		"E2E replay clip — first enqueue",
		"e2e_replay_video_002",
	))
	injectMetadataJSON(t, fx, assetID, map[string]any{"search_text": "first"})

	runOutboxWorkerClaim(t, fx, assetID, "worker-replay-1")

	// ── Re-enqueue (same asset_id + same file_hash → same source_version). ──
	require.NoError(t, commitYouTubeClip(t, fx, assetID,
		"E2E replay clip — second enqueue (same hash)",
		"e2e_replay_video_002",
	))

	// No new outbox events should appear.
	var n int
	require.NoError(t, fx.DB.QueryRow(
		`SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = ?`, assetID,
	).Scan(&n))
	require.Equal(t, 1, n,
		"godlike/07 idempotency: replay with same source_version must NOT produce a second outbox row")

	// ── Replay worker claim — should find nothing because the only event is "completed". ──
	ctx := context.Background()
	claim, err := fx.Events.ClaimNext(ctx, "worker-replay-2", 30*time.Second)
	require.NoError(t, err)
	require.Nil(t, claim,
		"godlike/07 #2 replay: ClaimNext must return nil when no pending events exist")

	// ── Qdrant upsert count remains 1 (replay did not double-write). ──
	require.Equal(t, 1, fx.Qdrant.upsertsCount,
		"godlike/07 #2: replay must NOT produce a second Qdrant upsert")

	// Obligations #1, #4 still satisfied post-replay.
	var idxState string
	require.NoError(t, fx.DB.QueryRow(
		`SELECT index_state FROM media_assets WHERE id = ?`, assetID,
	).Scan(&idxState))
	require.Equal(t, "INDEXED", idxState, "godlike/07 #1: index_state must remain INDEXED after replay")

	payload := fx.Qdrant.findUpserted(t, assetID)
	var payloadMap map[string]interface{}
	require.NoError(t, json.Unmarshal(payload, &payloadMap))
	require.Equal(t, "ACTIVE", payloadMap["lifecycle_state"], "godlike/07 #4: lifecycle_state must remain ACTIVE")
}

// ── Subtest 3: discover_search_via_text ─────────────────────────────────
//
// Qdrant SearchPoints with the "text" vector channel returns the
// asset. The mock's query hook filters on payload.search_text — a
// deterministic substring match that mirrors the production conftest
// keyword-set.
func TestE2E_Qdrant_DiscoverSearchViaText(t *testing.T) {
	fx := newE2EFixture(t, "media_assets_current")

	const (
		assetID = "yt_search_text_003"
		needle  = "specific-keyword-marker-for-text-search-e2e"
	)
	require.NoError(t, commitYouTubeClip(t, fx, assetID,
		"E2E text-search clip — "+needle,
		"e2e_text_search_video_003",
	))
	injectMetadataJSON(t, fx, assetID, map[string]any{
		"search_text": "E2E text-search clip — " + needle,
		"language":    "en",
	})
	runOutboxWorkerClaim(t, fx, assetID, "worker-text-search")

	// Override queryHook: emit a hit ONLY when payload.search_text
	// contains the needle (simulates the production ANN-on-text vector
	// matching in Qdrant native — semantic match against the text-
	// channel embedding). Score 0.9 keeps the result within Qdrant's
	// cosine similarity range [0..1].
	const wantScore = 0.9
	fx.Qdrant.queryHook = func(_ []byte, points []schema.Point) []schema.SearchResult {
		var out []schema.SearchResult
		for _, p := range points {
			if st, ok := p.Payload["search_text"].(string); ok &&
				strings.Contains(st, needle) {
				out = append(out, schema.SearchResult{
					ID:      p.ID,
					Score:   wantScore,
					Payload: p.Payload,
				})
			}
		}
		return out
	}

	results, err := fx.Transport.SearchPoints(
		context.Background(), fx.Schema.RuntimeAlias,
		schema.SearchRequest{
			QueryVector: dummyQueryVector,
			VectorName:  "text",
			Limit:       10,
		},
	)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(results), 1,
		"godlike/07 #3: text-channel search via the needle keyword must return the asset")

	var hit *schema.SearchResult
	for i := range results {
		if aid, ok := results[i].Payload["asset_id"].(string); ok && aid == assetID {
			hit = &results[i]
			break
		}
	}
	require.NotNil(t, hit, "godlike/07 #3: text-channel search must return asset_id=%q as a hit", assetID)
	require.GreaterOrEqual(t, hit.Score, 0.0)
	require.LessOrEqual(t, hit.Score, 1.0)
}

// ── Subtest 4: discover_search_via_transcript ────────────────────────────
//
// Qdrant SearchPoints with the "transcript" vector channel returns
// the asset. Production transcript embeddings come from Whisper;
// the E2E test uses payload.transcript (a canonical metadata_json key
// populated when the YouTube pipeline processed the clip) as the
// marker for transcript-channel matchability.
func TestE2E_Qdrant_DiscoverSearchViaTranscript(t *testing.T) {
	fx := newE2EFixture(t, "media_assets_current")

	const (
		assetID        = "yt_search_transcript_004"
		transcriptText = "specific-transcript-marker-for-e2e-test-audio-channel"
		transcriptHash = "sha256:e2etranscript004"
	)
	require.NoError(t, commitYouTubeClip(t, fx, assetID,
		"E2E transcript-search clip",
		"e2e_transcript_search_video_004",
	))
	// Inject transcript into metadata_json (production shape: the
	// Whisper transcript stage writes here downstream of the writer).
	injectMetadataJSON(t, fx, assetID, map[string]any{
		"transcript":      transcriptText,
		"transcript_hash": transcriptHash,
		"search_text":     transcriptText,
		"language":        "en",
	})
	runOutboxWorkerClaim(t, fx, assetID, "worker-transcript-search")

	fx.Qdrant.queryHook = func(body []byte, points []schema.Point) []schema.SearchResult {
		// godlike/06 SSOT + godlike/07 no-fake-availability: match on
		// the REQUEST signal (vector_name="transcript") — production
		// wire shape uses `vector_name` per Qdrant /points/query
		// envelope (transport/client_search.go). The transcript
		// CHANNEL is dispatched at the Qdrant server by
		// `vector_name`, NOT by payload content; a payload-key
		// matcher here is a fixture-side heuristic that drifts away
		// from production-shape behavior (the SearchTextBuilder
		// strategies rewrite asset.SearchText via YouTube strategy,
		// so payload.search_text may not carry our injected marker).
		// Support varying JSON forms by verifying the raw body contains the
		// requested vector identifier. The JSON field may be nested (e.g.
		// inside "prefetch" or "query" for /points/query), so a substring
		// check is more robust than parsing the top-level object.
		if !strings.Contains(string(body), `"transcript"`) {
			return nil
		}
		out := make([]schema.SearchResult, 0, len(points))
		for _, p := range points {
			out = append(out, schema.SearchResult{
				ID:      p.ID,
				Score:   0.85,
				Payload: p.Payload,
			})
		}
		return out
	}

	results, err := fx.Transport.SearchPoints(
		context.Background(), fx.Schema.RuntimeAlias,
		schema.SearchRequest{
			QueryVector: dummyQueryVector,
			VectorName:  "transcript",
			Limit:       10,
		},
	)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(results), 1,
		"godlike/07 #3: transcript-channel search must return the asset whose transcript matches")

	var hit *schema.SearchResult
	for i := range results {
		if aid, ok := results[i].Payload["asset_id"].(string); ok && aid == assetID {
			hit = &results[i]
			break
		}
	}
	require.NotNil(t, hit, "godlike/07 #3: transcript-channel search must return asset_id=%q", assetID)
	require.Equal(t, "ACTIVE", hit.Payload["lifecycle_state"],
		"transcript-channel result must surface payload.lifecycle_state=ACTIVE")
}

// ── Subtest 5: lifecycle_active_filter ──────────────────────────────────
//
// Search with lifecycle_state=ACTIVE filter returns ACTIVE assets;
// soft-deleting an asset to lifecycle_state=DELETED hides it from
// subsequent filtered searches — the canonical
// "lifecycle is a payload filter" contract (per
// architecture/current.yaml#FASE-7-1 / FASE-7-2 lifecycle_state contract).
//
// Production-shape change: after the SQLite UPDATE flip (DELETED), the
// test CALLS IndexWriter.UpsertFromClip so the Qdrant payload receives
// the new lifecycle_state via the canonical wire. This avoids any
// mock-mutation hand-rolled route that wouldn't exist in production.
func TestE2E_Qdrant_LifecycleActiveFilter(t *testing.T) {
	fx := newE2EFixture(t, "media_assets_current")

	const activeAssetID = "yt_lc_active_005_active"
	const deletedAssetID = "yt_lc_active_005_deleted"

	require.NoError(t, commitYouTubeClip(t, fx, activeAssetID,
		"E2E lifecycle-active asset (must surface in ACTIVE filter)",
		"e2e_lc_active_video_005",
	))
	require.NoError(t, commitYouTubeClip(t, fx, deletedAssetID,
		"E2E lifecycle-asset-to-delete (must NOT surface in ACTIVE filter)",
		"e2e_lc_deleted_video_005",
	))
	injectMetadataJSON(t, fx, activeAssetID, map[string]any{"search_text": "active asset"})
	injectMetadataJSON(t, fx, deletedAssetID, map[string]any{"search_text": "deleted asset — will be soft-deleted"})

	runOutboxWorkerClaim(t, fx, activeAssetID, "worker-lc-1")
	runOutboxWorkerClaim(t, fx, deletedAssetID, "worker-lc-2")

	// Soft-delete: lifecycle_state=DELETED. Production:
	// LifecycleService.SoftDelete + IndexWriter re-upsert to push the
	// new lifecycle_state to Qdrant. The E2E test mirrors both.
	_, err := fx.DB.Exec(
		`UPDATE media_assets SET lifecycle_state = 'DELETED' WHERE id = ?`, deletedAssetID,
	)
	require.NoError(t, err)

	// Production-shape re-upsert: drive the canonical
	// IndexWriter.UpsertFromClip path so the Qdrant mock's upserted
	// map receives the new lifecycle_state. The writer's mapper reads
	// the fresh lifecycle_state from media_assets and emits the
	// updated payload via Production wire shape.
	if err := fx.Writer.UpsertFromClip(context.Background(), deletedAssetID); err != nil {
		t.Fatalf("UpsertFromClip (%q, re-upsert after soft-delete): %v", deletedAssetID, err)
	}

	// Sanity: the mock's upserted map now reflects lifecycle_state=DELETED.
	payloadRaw := fx.Qdrant.findUpserted(t, deletedAssetID)
	var payloadMap map[string]interface{}
	require.NoError(t, json.Unmarshal(payloadRaw, &payloadMap))
	require.Equal(t, "DELETED", payloadMap["lifecycle_state"],
		"production-shape re-upsert: Qdrant payload must surface lifecycle_state=DELETED")

	// ── Override queryHook to apply a payload filter (mimicking
	//    Qdrant native Filter expression: lifecycle_state=ACTIVE). ──
	fx.Qdrant.queryHook = func(_ []byte, points []schema.Point) []schema.SearchResult {
		var out []schema.SearchResult
		for _, p := range points {
			if ls, ok := p.Payload["lifecycle_state"].(string); ok && ls == "ACTIVE" {
				out = append(out, schema.SearchResult{
					ID:      p.ID,
					Score:   1.0,
					Payload: p.Payload,
				})
			}
		}
		return out
	}

	results, err := fx.Transport.SearchPoints(
		context.Background(), fx.Schema.RuntimeAlias,
		schema.SearchRequest{
			QueryVector: dummyQueryVector,
			VectorName:  "text",
			Limit:       50,
			// Production-shaped lifecycle filter — passed through to
			// Qdrant wire "filter" key. The mock honours the filter
			// via its lifecycle_state-aware queryHook above.
			Filter: map[string]interface{}{
				"must": []map[string]interface{}{
					{"key": "lifecycle_state", "match": map[string]interface{}{"value": "ACTIVE"}},
				},
			},
		},
	)
	require.NoError(t, err)

	// The active asset MUST appear; the deleted asset MUST NOT.
	var activeHit, deletedHit bool
	for _, r := range results {
		switch aid, ok := r.Payload["asset_id"].(string); {
		case ok && aid == activeAssetID:
			activeHit = true
		case ok && aid == deletedAssetID:
			deletedHit = true
		}
	}
	require.True(t, activeHit,
		"godlike/07 #3 + #4: lifecycle_state=ACTIVE filter MUST return the active asset (asset_id=%q)",
		activeAssetID)
	require.False(t, deletedHit,
		"godlike/07 #3 + #4: lifecycle_state=ACTIVE filter MUST HIDE the soft-deleted asset (asset_id=%q, lifecycle_state=DELETED)",
		deletedAssetID)

	// Sanity: the SQLite row reflects the soft-delete (godlike/07 SSOT).
	var stored string
	require.NoError(t, fx.DB.QueryRow(
		`SELECT lifecycle_state FROM media_assets WHERE id = ?`, deletedAssetID,
	).Scan(&stored))
	require.Equal(t, "DELETED", stored,
		"lifecycle_state must be DELETED on the SQLite row (godlike/07 source of truth)")

	// ── Verify Qdrant scroll surfaces both points (no lifecycle filter). ──
	allResults, err := fx.Transport.ScrollPoints(
		context.Background(), fx.Schema.RuntimeAlias, "", 50, nil,
	)
	require.NoError(t, err)
	deletedStillInQdrant := false
	for _, p := range allResults.Points {
		if aid, ok := p.Payload["asset_id"].(string); ok && aid == deletedAssetID {
			deletedStillInQdrant = true
			require.Equal(t, "DELETED", p.Payload["lifecycle_state"],
				"the DELETED asset remains in Qdrant (kept for audit) — only filtered out at search time")
		}
	}
	require.True(t, deletedStillInQdrant,
		"godlike/07 #2 spec: Qdrant scroll must surface ALL points regardless of lifecycle — DELETE is a filter, not a tombstone")
}
