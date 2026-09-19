package wiring

// render_attempt_analytics_wiring_test.go verifies the coarse per-attempt
// analytics row (render_ms/encode_ms in render_attempt_analytics) is persisted
// through the live wiring recorder.
//
// The parallel Chronon-phase half of this test was removed together with the
// orphan internal/app/wiring/chronon package (zero production importers). The
// live granular-phase path is
// internal/app/wiring/rendering/metrics.go::NewChrononMetricsAdapter, pinned by
// internal/app/wiring/rendering/metrics_integration_test.go.

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// renderAttemptAnalyticsWiringSchema is the canonical render_attempt_analytics
// DDL (migration 215 + the completion-wait columns from migration 227) needed
// by the SQLite recorder.
const renderAttemptAnalyticsWiringSchema = `
CREATE TABLE render_attempt_analytics (
    attempt_id      TEXT PRIMARY KEY,
    job_id          TEXT NOT NULL DEFAULT '',
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
    recorded_at     TEXT NOT NULL
);`

func TestWireRenderAttemptRecorderNilDBIsNil(t *testing.T) {
	if got := wireRenderAttemptRecorder(nil, zap.NewNop()); got != nil {
		t.Fatalf("wireRenderAttemptRecorder(nil DB) = non-nil, want nil")
	}
}

// TestWireRenderAttemptRecorderPersistsCertifiedRow pins the coarse per-attempt
// contract: the certified render_ms/encode_ms land verbatim in exactly one
// render_attempt_analytics row keyed by attempt_id.
func TestWireRenderAttemptRecorderPersistsCertifiedRow(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(renderAttemptAnalyticsWiringSchema); err != nil {
		t.Fatal(err)
	}

	recorder := wireRenderAttemptRecorder(db, zap.NewNop())
	if recorder == nil {
		t.Fatal("wireRenderAttemptRecorder over a real DB returned nil")
	}

	// render_ms/encode_ms come verbatim from the certified queue artifact
	// (the worker-measured wall times).
	attempt := scriptgen.RenderAttemptAnalytics{
		AttemptID:  "attempt-parallel-1",
		JobID:      "job-parallel-1",
		Content:    capoverlay.ContentCounts{Phrases: 1, Words: 2, Images: 3, Leaks: 4},
		RenderMS:   24971,
		EncodeMS:   554,
		Width:      1920,
		Height:     1080,
		FPSNum:     30,
		FPSDen:     1,
		FrameCount: 1350,
		DurationUS: 45000000,
		SizeBytes:  12345,
		SHA256:     "sha-certified",
	}
	if err := recorder.RecordAttempt(context.Background(), attempt); err != nil {
		t.Fatal(err)
	}

	var attemptCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM render_attempt_analytics WHERE attempt_id='attempt-parallel-1'`).Scan(&attemptCount); err != nil {
		t.Fatal(err)
	}
	if attemptCount != 1 {
		t.Fatalf("render_attempt_analytics rows = %d, want 1", attemptCount)
	}
	var renderMS, encodeMS int64
	if err := db.QueryRow(`SELECT render_ms, encode_ms FROM render_attempt_analytics WHERE attempt_id='attempt-parallel-1'`).Scan(&renderMS, &encodeMS); err != nil {
		t.Fatal(err)
	}
	if renderMS != 24971 || encodeMS != 554 {
		t.Fatalf("render_ms/encode_ms = %d/%d, want 24971/554", renderMS, encodeMS)
	}
}
