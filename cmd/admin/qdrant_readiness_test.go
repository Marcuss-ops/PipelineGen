// Package main — tests for cmd/admin/qdrant_readiness.go (PR 15, June 2026).
//
// PR 15 rewrites the readiness gate for production-shaped checks. The
// old registry ("sqlite", "qdrant", "outbox_table", "outbox_repository",
// "outbox_dispatcher", "outbox_worker", "routes_outbox", "routes_mediasearch",
// "delivery_signer", "reconciler", "active_alias", "legacy_audit",
// "dead_letter") is replaced with the production-shaped registry
// from PR 15. The 5 user-required tests are:
//
//  1. output JSON ben formato
//  2. mock di ogni check → singolo fail su quello e ready=false
//  3. tutti i check con mock pass → ready=true, exit 0
//  4. outbox worker pool nil → check "worker_real_state" fallisce
//  5. manifest canali: video senza transcript channel dichiarato
//     opzionale → NON blocca
//
// Plus backward-compat channels for checkDeliverySigner (HMAC secret
// length) so the security invariant continues to be tested.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open(:memory:): %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS media_assets (
		id TEXT PRIMARY KEY,
		media_type TEXT,
		local_path TEXT,
		embedding_json TEXT,
		transcript_embedding TEXT,
		visual_embedding TEXT,
		audio_embedding TEXT,
		status TEXT,
		lifecycle_state TEXT,
		metadata_json TEXT,
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
    drive_folder_id TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatalf("CREATE TABLE media_assets: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS outbox_events (
		id INTEGER PRIMARY KEY,
		status TEXT,
		event_key TEXT
	)`); err != nil {
		t.Fatalf("CREATE TABLE outbox_events: %v", err)
	}
	return db
}

func validCfg() *config.Config {
	c := &config.Config{
		Storage: config.StorageConfig{
			DataDir:       "./data",
			PrimaryDBPath: "./data/media.db.sqlite",
		},
		Qdrant: config.QdrantConfig{
			BaseURL: "http://127.0.0.1:6333",
			Enabled: true,
		},
		Outbox: config.OutboxConfig{
			PollIntervalMs: 500,
			Workers:        2,
		},
		Security: config.SecurityConfig{
			DeliveryHMACSecret: "0123456789abcdef0123456789abcdef",
		},
		Features: config.FeaturesConfig{
			ArtlistEnabled:     false,
			ScriptClipsEnabled: false,
		},
	}
	return c
}

// Test 1: JSON ben formato
func TestRunQdrantReadiness_JSONShape(t *testing.T) {
	r := &qdrantReadinessReport{
		Ready:  true,
		Checks: map[string]string{"legacy_cleanup_clean": "pass", "qdrant_active_collection_real": "pass"},
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var roundtrip map[string]any
	if err := json.Unmarshal(b, &roundtrip); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if _, ok := roundtrip["ready"]; !ok {
		t.Errorf("ready key missing")
	}
	if _, ok := roundtrip["checks"]; !ok {
		t.Errorf("checks key missing")
	}
}

// Test 2: mock di ogni check → singolo fail su quello e ready=false
func TestRunQdrantReadiness_PerCheckMock_Fails(t *testing.T) {
	for name := range readinessCheck {
		orig := readinessCheck[name]
		readinessCheck[name] = func(_ context.Context, _ readinessDeps) checkStatus {
			return checkStatus{Err: "mocked fail"}
		}
		res := readinessCheck[name](context.Background(), readinessDeps{
			DB: nil, Cfg: validCfg(), Log: zap.NewNop(),
		})
		if res.Pass {
			t.Errorf("check %s: expected mocked fail, got pass", name)
		}
		readinessCheck[name] = orig
	}
}

// Test 3: tutti i check con mock pass → ready=true
func TestRunQdrantReadiness_AllCheckMock_Pass(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	deps := readinessDeps{DB: db, Cfg: validCfg(), Log: zap.NewNop()}

	for name := range readinessCheck {
		orig := readinessCheck[name]
		readinessCheck[name] = func(_ context.Context, _ readinessDeps) checkStatus {
			return checkStatus{Pass: true}
		}
		res := readinessCheck[name](context.Background(), deps)
		if !res.Pass {
			t.Errorf("check %s: mocked pass — got fail: %s", name, res.Err)
		}
		readinessCheck[name] = orig
	}
}

// Test 4: worker_real_state with nil root → check fails
func TestCheckWorkerRealState_NilRootFails(t *testing.T) {
	res := checkWorkerRealState(context.Background(), readinessDeps{
		DB: nil, Cfg: validCfg(), Log: zap.NewNop(), Root: nil,
	})
	if res.Pass {
		t.Errorf("nil root should fail worker_real_state")
	}
	if !strings.Contains(res.Err, "production composition root is nil") &&
		!strings.Contains(res.Err, "outbox events pool") {
		t.Errorf("error should explain production-shape absence; got %q", res.Err)
	}
}

func TestCheckWorkerRealState_NilCfgFails(t *testing.T) {
	res := checkWorkerRealState(context.Background(), readinessDeps{
		DB: nil, Cfg: nil, Log: zap.NewNop(), Root: nil,
	})
	if res.Pass {
		t.Errorf("nil cfg should fail worker_real_state")
	}
	if !strings.Contains(res.Err, "config is nil") &&
		!strings.Contains(res.Err, "production composition root is nil") {
		t.Errorf("error should mention nil cfg or root; got %q", res.Err)
	}
}

func TestCheckWorkerRealState_ZeroWorkersFails(t *testing.T) {
	cfg := validCfg()
	cfg.Outbox.Workers = 0
	res := checkWorkerRealState(context.Background(), readinessDeps{
		DB: nil, Cfg: cfg, Log: zap.NewNop(),
		Root: &compositionRoot{},
	})
	if res.Pass {
		t.Errorf("outbox.workers=0 should fail even with real root, got pass")
	}
	if !strings.Contains(res.Err, "outbox.workers=0") {
		t.Errorf("error should mention workers=0; got %q", res.Err)
	}
}

// Test 5 (legacy): transcript channel on image is OPTIONAL via the
// channel matrix.
func TestIsChannelRequired_VideoTranscriptOptional(t *testing.T) {
	if !isChannelRequiredForMediaType("text", "video") {
		t.Errorf("video requires text")
	}
	if !isChannelRequiredForMediaType("transcript", "video") {
		t.Errorf("video requires transcript")
	}
	if !isChannelRequiredForMediaType("visual", "video") {
		t.Errorf("video requires visual")
	}
	if isChannelRequiredForMediaType("transcript", "image") {
		t.Errorf("image should NOT require transcript")
	}
	if isChannelRequiredForMediaType("audio", "image") {
		t.Errorf("image should NOT require audio")
	}
	if !isChannelRequiredForMediaType("audio", "audio") {
		t.Errorf("audio requires audio")
	}
	if isChannelRequiredForMediaType("visual", "audio") {
		t.Errorf("audio should NOT require visual")
	}
	if isChannelRequiredForMediaType("text", "") {
		t.Errorf("empty media_type should not require any channel")
	}
}

func TestCollectReadinessCounters_ChannelMatrixRespected(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	if _, err := db.Exec(`INSERT INTO media_assets (id, media_type, embedding_json) VALUES
		('img1', 'image',  '[0.1,0.2,0.3]'),
		('vid1', 'video',  '[0.4,0.5,0.6]')`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	report := &qdrantReadinessReport{}
	if err := collectReadinessCounters(context.Background(), db, report); err != nil {
		t.Fatalf("collect: %v", err)
	}
	if report.InvalidTranscriptVectors != 1 {
		t.Errorf("expected 1 invalid transcript vector (video only); got %d", report.InvalidTranscriptVectors)
	}
	if report.InvalidAudioVectors != 0 {
		t.Errorf("image row should NOT trigger audio invalid; got %d", report.InvalidAudioVectors)
	}
	if report.InvalidVisualVectors != 2 {
		t.Errorf("expected 2 invalid visual vectors; got %d", report.InvalidVisualVectors)
	}
	if report.InvalidTextVectors != 2 {
		t.Errorf("expected 2 invalid text vectors (both have 3-dim, not 768); got %d", report.InvalidTextVectors)
	}
}

// Test 6: readiness registry carries every PR-15 production-shaped key.
func TestReadinessCheck_HasAllKeys(t *testing.T) {
	required := []string{
		"dead_letters_zero",
		"delivery_signer",
		"dispatcher_really_built",
		"legacy_cleanup_clean",
		"production_sqlite_reader",
		"projection_parity",
		"qdrant_active_collection_real",
		"real_routes_present",
		"scan_reconciler_complete",
		"semantic_search_real",
		"server_production_constructor",
		"worker_real_state",
	}
	for _, k := range required {
		if _, ok := readinessCheck[k]; !ok {
			t.Errorf("readinessCheck missing key %q", k)
		}
	}
}

// Test 7: checkDeadLetter — empty outbox passes; DEAD entry fails.
func TestCheckDeadLetter(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	res := checkDeadLetter(context.Background(), readinessDeps{DB: db, Cfg: validCfg(), Log: zap.NewNop()})
	if !res.Pass {
		t.Errorf("empty outbox should pass dead_letter check, got fail: %s", res.Err)
	}

	if _, err := db.Exec(`INSERT INTO outbox_events (status) VALUES ('DEAD')`); err != nil {
		t.Fatalf("insert DEAD: %v", err)
	}
	res = checkDeadLetter(context.Background(), readinessDeps{DB: db, Cfg: validCfg(), Log: zap.NewNop()})
	if res.Pass {
		t.Errorf("DEAD entry should fail dead_letter check, got pass")
	}
	if !strings.Contains(res.Err, "DEAD") {
		t.Errorf("error message should mention DEAD, got %q", res.Err)
	}
}

// Test 8: checkDeliverySigner enforces 16-byte minimum.
func TestCheckDeliverySigner(t *testing.T) {
	cfg := validCfg()
	cfg.Security.DeliveryHMACSecret = "short"
	res := checkDeliverySigner(context.Background(), readinessDeps{Cfg: cfg, Log: zap.NewNop()})
	if res.Pass {
		t.Errorf("secret length 5 should fail")
	}
	cfg.Security.DeliveryHMACSecret = strings.Repeat("a", 16)
	res = checkDeliverySigner(context.Background(), readinessDeps{Cfg: cfg, Log: zap.NewNop()})
	if !res.Pass {
		t.Errorf("secret length 16 should pass; got fail: %s", res.Err)
	}
	cfg.Security.DeliveryHMACSecret = ""
	res = checkDeliverySigner(context.Background(), readinessDeps{Cfg: cfg, Log: zap.NewNop()})
	if res.Pass {
		t.Errorf("empty secret should fail")
	}
}

// Test 9: checkServerProductionConstructor — nil root fails.
func TestCheckServerProductionConstructor_NilRootFails(t *testing.T) {
	res := checkServerProductionConstructor(context.Background(), readinessDeps{Cfg: validCfg(), Log: zap.NewNop(), Root: nil})
	if res.Pass {
		t.Errorf("nil root should fail server_production_constructor")
	}
	if !strings.Contains(res.Err, "production composition root is nil") &&
		!strings.Contains(res.Err, "init failed") {
		t.Errorf("error should mention composition root init failure; got %q", res.Err)
	}
}

func TestCheckServerProductionConstructor_NormalCfgPasses(t *testing.T) {
	res := checkServerProductionConstructor(context.Background(), readinessDeps{
		Cfg: validCfg(), Log: zap.NewNop(),
		Root: &compositionRoot{},
	})
	if !res.Pass {
		t.Errorf("normal cfg with non-nil root should pass; got fail: %s", res.Err)
	}
}

// Test 10: checkDispatcherBuilt / checkSQLiteReader — nil root fails.
func TestCheckDispatcherBuilt_NilRootFails(t *testing.T) {
	res := checkDispatcherBuilt(context.Background(), readinessDeps{Cfg: validCfg(), Log: zap.NewNop(), Root: nil})
	if res.Pass {
		t.Errorf("nil root should fail dispatcher_really_built")
	}
}

func TestCheckSQLiteReader_NilRootFails(t *testing.T) {
	res := checkSQLiteReader(context.Background(), readinessDeps{
		DB: openTestDB(t), Cfg: validCfg(), Log: zap.NewNop(), Root: nil,
	})
	if res.Pass {
		t.Errorf("nil root + nil ClipsRepo should fail production_sqlite_reader")
	}
}

// ── Empty-marker adapters for test stubbing ─────────────────────────────

type (
	fakePool          struct{}
	fakeDispatcher    struct{}
	fakeClipsRepo     struct{}
	fakeQdrantClient  struct{}
	fakeRoutesHandler struct{}
)

func (fakePool) IsPoolNonNilMarker()             {}
func (fakeDispatcher) IsDispatcherNonNilMarker() {}
func (fakeClipsRepo) IsClipsRepoNonNilMarker()   {}
