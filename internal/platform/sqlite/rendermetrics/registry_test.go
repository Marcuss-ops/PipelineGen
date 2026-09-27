package rendermetrics

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

func mustDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	return db
}

const schema = `
CREATE TABLE render_attempt_analytics (
    attempt_id      TEXT PRIMARY KEY,
    job_id          TEXT NOT NULL DEFAULT '',
    item_id         TEXT NOT NULL DEFAULT '',
    phrase_count    INTEGER NOT NULL DEFAULT 0,
    word_count      INTEGER NOT NULL DEFAULT 0,
    image_count     INTEGER NOT NULL DEFAULT 0,
    leak_count      INTEGER NOT NULL DEFAULT 0,
    render_ms       INTEGER NOT NULL DEFAULT 0,
    encode_ms       INTEGER NOT NULL DEFAULT 0,
    completion_wait_ms INTEGER NOT NULL DEFAULT 0,
    polling_sleep_ms INTEGER NOT NULL DEFAULT 0,
    polling_interval_ms INTEGER NOT NULL DEFAULT 0,
    poll_count       INTEGER NOT NULL DEFAULT 0,
    width           INTEGER NOT NULL DEFAULT 0,
    height          INTEGER NOT NULL DEFAULT 0,
    fps_num         INTEGER NOT NULL DEFAULT 0,
    fps_den         INTEGER NOT NULL DEFAULT 0,
    frame_count     INTEGER NOT NULL DEFAULT 0,
    duration_us     INTEGER NOT NULL DEFAULT 0,
    size_bytes      INTEGER NOT NULL DEFAULT 0,
    sha256          TEXT NOT NULL DEFAULT '',
    drive_file_id   TEXT NOT NULL DEFAULT '',
    drive_link      TEXT NOT NULL DEFAULT '',
    backend         TEXT NOT NULL DEFAULT '',
    chronon_version TEXT NOT NULL DEFAULT '',
    profile_id      TEXT NOT NULL DEFAULT '',
    codec           TEXT NOT NULL DEFAULT '',
    codec_profile   TEXT NOT NULL DEFAULT '',
    container       TEXT NOT NULL DEFAULT '',
    pixel_format    TEXT NOT NULL DEFAULT '',
    materialize_ms  INTEGER NOT NULL DEFAULT 0,
    plan_ms         INTEGER NOT NULL DEFAULT 0,
    probe_ms        INTEGER NOT NULL DEFAULT 0,
    hash_ms         INTEGER NOT NULL DEFAULT 0,
    upload_ms       INTEGER NOT NULL DEFAULT 0,
    drive_publish_ms INTEGER NOT NULL DEFAULT 0,
    metrics_json    TEXT NOT NULL DEFAULT '',
    chronon_telemetry TEXT NOT NULL DEFAULT '',
    chronon_timing_storage_key TEXT NOT NULL DEFAULT '',
    chronon_timing_url TEXT NOT NULL DEFAULT '',
    chronon_timing_sha256 TEXT NOT NULL DEFAULT '',
    chronon_timing_size_bytes INTEGER NOT NULL DEFAULT 0,
    chronon_timing_content_type TEXT NOT NULL DEFAULT '',
    recorded_at     TEXT NOT NULL
);`

// TestRecordAttemptUpsertsIdempotently pins the idempotency key contract:
// recording the same attempt_id twice converges on one row (updated, not
// duplicated).
func TestRecordAttemptUpsertsIdempotently(t *testing.T) {
	db := mustDB(t)
	reg, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	a := scriptgen.RenderAttemptAnalytics{
		AttemptID:         "attempt-1",
		JobID:             "job-1",
		ItemID:            "phrase-hello",
		Content:           capoverlay.ContentCounts{Phrases: 1, Words: 2, Images: 3, Leaks: 4},
		RenderMS:          100,
		EncodeMS:          50,
		CompletionWaitMS:  2100,
		PollingSleepMS:    2000,
		PollingIntervalMS: 2000,
		PollCount:         2,
		Width:             1920,
		Height:            1080,
		Backend:           "vulkan",
		ChrononVersion:    "chronon-0.9.1",
		MaterializeMS:     420,
		PlanMS:            12,
		SHA256:            "sha-1",
		MetricsJSON:       `{"gpu_lane_wait_ms":820}`,
		ChrononTelemetryJSON: `{"job":{"plan_compile_ms":4.1}}`,
		ChrononTimingStorageKey: "chronon/timing/abc.json",
	}
	if err := reg.RecordAttempt(ctx, a); err != nil {
		t.Fatal(err)
	}
	// Re-record with updated facts: same attempt_id, different sha256.
	a.SHA256 = "sha-2"
	a.MetricsJSON = `{"gpu_lane_wait_ms":900}`
	if err := reg.RecordAttempt(ctx, a); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM render_attempt_analytics`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("rows = %d, want 1 (upsert keyed by attempt_id)", count)
	}
	var gotSHA, gotItemID, gotBackend, gotChrononVersion, gotMetrics, gotTelemetry, gotTimingKey string
	var phrases, words, images, leaks, renderMS, encodeMS, completionWaitMS, pollingSleepMS, pollingIntervalMS, pollCount int
	var width, height, matMS, planMS int
	if err := db.QueryRow(`SELECT sha256, item_id, backend, chronon_version, metrics_json, chronon_telemetry, chronon_timing_storage_key, phrase_count, word_count, image_count, leak_count, render_ms, encode_ms, completion_wait_ms, polling_sleep_ms, polling_interval_ms, poll_count, width, height, materialize_ms, plan_ms FROM render_attempt_analytics WHERE attempt_id='attempt-1'`).
		Scan(&gotSHA, &gotItemID, &gotBackend, &gotChrononVersion, &gotMetrics, &gotTelemetry, &gotTimingKey, &phrases, &words, &images, &leaks, &renderMS, &encodeMS, &completionWaitMS, &pollingSleepMS, &pollingIntervalMS, &pollCount, &width, &height, &matMS, &planMS); err != nil {
		t.Fatal(err)
	}
	if gotSHA != "sha-2" || gotItemID != "phrase-hello" || gotBackend != "vulkan" || gotChrononVersion != "chronon-0.9.1" || gotMetrics != `{"gpu_lane_wait_ms":900}` || gotTelemetry != `{"job":{"plan_compile_ms":4.1}}` || gotTimingKey != "chronon/timing/abc.json" ||
		phrases != 1 || words != 2 || images != 3 || leaks != 4 || renderMS != 100 || encodeMS != 50 || completionWaitMS != 2100 || pollingSleepMS != 2000 || pollingIntervalMS != 2000 || pollCount != 2 ||
		width != 1920 || height != 1080 || matMS != 420 || planMS != 12 {
		t.Fatalf("row = sha=%s item=%s backend=%s chronon=%s metrics=%s telemetry=%s timing=%s counts=%d/%d/%d/%d render=%d encode=%d completion_wait=%d polling_sleep=%d interval=%d polls=%d wh=%d/%d mat=%d plan=%d", gotSHA, gotItemID, gotBackend, gotChrononVersion, gotMetrics, gotTelemetry, gotTimingKey, phrases, words, images, leaks, renderMS, encodeMS, completionWaitMS, pollingSleepMS, pollingIntervalMS, pollCount, width, height, matMS, planMS)
	}
}

// TestRecordAttemptRequiresAttemptID pins the fail-closed contract: a record
// without an attempt_id is rejected, never silently persisted.
func TestRecordAttemptRequiresAttemptID(t *testing.T) {
	db := mustDB(t)
	reg, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.RecordAttempt(context.Background(), scriptgen.RenderAttemptAnalytics{}); err == nil {
		t.Fatal("empty attempt_id must fail closed")
	}
}

// TestNewRejectsNilDB pins the adapter constructor contract.
func TestNewRejectsNilDB(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("nil db must be rejected")
	}
}
