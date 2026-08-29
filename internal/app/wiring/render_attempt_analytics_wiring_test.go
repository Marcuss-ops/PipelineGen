package wiring

// render_attempt_analytics_wiring_test.go verifies the parallel analytics
// contract: the coarse per-attempt row (render_ms/encode_ms in
// render_attempt_analytics) and the granular exclusive-wall phases
// (chronon.* in performance_operations) are both persisted for the same
// render — each in its own existing table, with no new table created.

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
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

// TestParallelAnalyticsPersistenceNoNewTables pins the parallel contract: the
// same render produces a render_attempt_analytics row (render_ms/encode_ms
// from the certified artifact) AND the granular chronon.* phase rows in
// performance_operations, and sqlite_master still holds exactly the two
// canonical analytics tables — nothing new was created.
func TestParallelAnalyticsPersistenceNoNewTables(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(renderAttemptAnalyticsWiringSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(chrononMetricsWiringSchema); err != nil {
		t.Fatal(err)
	}

	recorder := wireRenderAttemptRecorder(db, zap.NewNop())
	if recorder == nil {
		t.Fatal("wireRenderAttemptRecorder over a real DB returned nil")
	}
	adapter := wireChrononMetricsAdapter(db, zap.NewNop())
	if adapter == nil {
		t.Fatal("wireChrononMetricsAdapter over a real DB returned nil")
	}

	// Coarse per-attempt row: render_ms/encode_ms come verbatim from the
	// certified queue artifact (the worker-measured wall times).
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

	// Granular exclusive-wall phases from the same render's sidecar, published
	// through the adapter inside a run-bound context (the job-worker path).
	doc, err := cliprender.ParseChrononSidecar([]byte(chrononMetricsWiringSidecar))
	if err != nil {
		t.Fatal(err)
	}
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{
		JobID:     "job-parallel-1",
		AttemptID: "attempt-parallel-1",
	})
	adapter.Publish(kernobs.WithRun(context.Background(), run), doc, cliprender.ChrononMetricsPublishOptions{
		SourceDurationMS: 45000,
		Width:            1920,
		Height:           1080,
		FPS:              30,
	})

	// 1) render_attempt_analytics holds exactly one row with the certified
	// render_ms/encode_ms.
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

	// 2) performance_operations holds the granular phases for the same job.
	var opCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM performance_operations WHERE job_id='job-parallel-1'`).Scan(&opCount); err != nil {
		t.Fatal(err)
	}
	if opCount != 7 {
		t.Fatalf("performance_operations rows = %d, want 7 (one per measured exclusive-wall phase)", opCount)
	}

	// 3) No new table was created: sqlite_master holds exactly the two
	// canonical analytics tables (plus nothing else).
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	wantTables := []string{"performance_operations", "render_attempt_analytics"}
	if len(tables) != len(wantTables) {
		t.Fatalf("tables = %v, want exactly %v (no new table created)", tables, wantTables)
	}
	for i := range wantTables {
		if tables[i] != wantTables[i] {
			t.Fatalf("tables = %v, want exactly %v (no new table created)", tables, wantTables)
		}
	}
}
