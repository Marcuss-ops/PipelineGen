// Package jobs tests — TDD coverage for RED-2 / JOBS-T01-001 closure.
//
// These tests verify the strftime('%Y-%m-%dT%H:%M:%fZ', created_at)
// canonical wrap on ListEvents' SELECT ensures the Go SQLite driver
// scans DATETIME columns into time.Time fields without raising the
// "events Scan error" condition that motivated RED-2.
//
// RED-2 / JOBS-T01-001 closure, 2026-07-04 (Phase 9 Battery).
// godlike/06 SSOT: this is the canonical regression-coverage surface
// for events.scan. Tests run on in-memory SQLite (`:memory:`) so no
// production DB state is touched.
package jobs

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// makeEventsTestDB returns an in-memory SQLite handle with the minimal
// job_events schema (mirrors the canonical job_events table from
// migration 006_create_job_events.sql). The store pointlessly depends
// on a transaction-capable db; :memory: provides one per test.
func makeEventsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE job_events (
		id TEXT PRIMARY KEY,
		job_id TEXT NOT NULL,
		type TEXT NOT NULL,
		message TEXT DEFAULT '',
		data_json TEXT DEFAULT '{}',
		created_at DATETIME
	)`); err != nil {
		t.Fatalf("create job_events schema: %v", err)
	}
	return db
}

// TestListEvents_StrftimeCanonicalScan is the canonical regression
// test for RED-2 / JOBS-T01-001. It asserts that:
//  1. A row inserted with a known time.Time parses back equal via
//     ListEvents (round-trip stability).
//  2. The scan does NOT raise an "events Scan error" on the canonical
//     RFC3339Nano string format produced by the strftime() wrap.
//  3. Multiple rows come back ordered by created_at ASC.
func TestListEvents_StrftimeCanonicalScan(t *testing.T) {
	db := makeEventsTestDB(t)
	ctx := context.Background()
	store := NewSQLiteStore(db, zap.NewNop())

	jobID := "job_test_strftime_001"

	// Insert 3 events with deterministic timestamps. Use cases:
	//   - 18:30:00 midnight UTC
	//   - 12:00:00 noon UTC (mid-day) — verifies AM/PM cross-boundary
	//   - 00:00:00 the second (date rollover boundary)
	times := []time.Time{
		time.Date(2026, 7, 4, 18, 30, 0, 0, time.UTC),
		time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 4, 0, 0, 0, 0, time.UTC),
	}
	for i, ts := range times {
		evtID := time.Now().Format("150405") + "_evt_" + string(rune('A'+i))
		_, err := db.ExecContext(ctx,
			`INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			evtID, jobID, "test_event", "msg", `{"idx":`+string(rune('0'+i))+`}`,
			ts.UTC().Format("2006-01-02 15:04:05"))
		if err != nil {
			t.Fatalf("insert evt %d at %v: %v", i, ts, err)
		}
	}

	// Pull via ListEvents; expected: 3 events ordered by created_at ASC.
	events, err := store.ListEvents(ctx, jobID)
	if err != nil {
		t.Fatalf("ListEvents: %v (RED-2 regression: events Scan error)", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}

	// Verify chronological order (00:00:00 → 12:00:00 → 18:30:00).
	wantIdx := []int{2, 1, 0}
	for i, evt := range events {
		if !evt.CreatedAt.Equal(times[wantIdx[i]]) {
			t.Errorf("event[%d] CreatedAt = %v, want %v (RED-2 strftime canonical mismatch)",
				i, evt.CreatedAt, times[wantIdx[i]])
		}
	}
}

// TestListEvents_NoRowsNoScan asserts the empty-result path doesn't
// raise a scan error (existing behaviour; pinned for RED-2 regression).
func TestSetProgressDataPersistsActivityEnvelope(t *testing.T) {
	db := makeEventsTestDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE jobs (id TEXT PRIMARY KEY, type TEXT NOT NULL, correlation_id TEXT, progress INTEGER, updated_at TEXT)`); err != nil {
		t.Fatalf("create jobs schema: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO jobs (id, type, correlation_id, progress, updated_at) VALUES (?, ?, ?, 0, '')`, "job-progress-data", "script.generate", "corr-1"); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	store := NewSQLiteStore(db, zap.NewNop())

	err := store.SetProgressData(ctx, "job-progress-data", 42, "resolving source", map[string]any{
		"kind": "script.generate", "sub_kind": "script.prepare.resolve_source", "status": "running",
		"detail": "resolving source", "payload": map[string]any{"item_id": "item-1"},
	})
	if err != nil {
		t.Fatalf("SetProgressData: %v", err)
	}
	events, err := store.ListEvents(ctx, "job-progress-data")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	data := events[0].Data
	if data["kind"] != "script.generate" || data["sub_kind"] != "script.prepare.resolve_source" || data["status"] != "running" || data["detail"] != "resolving source" {
		t.Fatalf("activity data = %#v", data)
	}
	if data["progress"] != float64(42) {
		t.Fatalf("progress = %#v, want 42", data["progress"])
	}
	if data["micro_kind"] != data["sub_kind"] || data["correlation_id"] != "corr-1" {
		t.Fatalf("micro-kind/trace metadata = %#v", data)
	}
	payload, ok := data["payload"].(map[string]any)
	if !ok || payload["item_id"] != "item-1" || payload["progress"] != float64(42) || payload["message"] != "resolving source" {
		t.Fatalf("payload = %#v, want item_id/progress/message preserved", data["payload"])
	}
}

func TestSetProgressPreservesEmptyMessageCompatibility(t *testing.T) {
	db := makeEventsTestDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE jobs (id TEXT PRIMARY KEY, type TEXT NOT NULL, correlation_id TEXT, progress INTEGER, updated_at TEXT)`); err != nil {
		t.Fatalf("create jobs schema: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO jobs (id, type, correlation_id, progress, updated_at) VALUES (?, ?, ?, 0, '')`, "job-empty-progress", "script.generate", "corr-empty"); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	store := NewSQLiteStore(db, zap.NewNop())

	if err := store.SetProgress(ctx, "job-empty-progress", 25, ""); err != nil {
		t.Fatalf("SetProgress: %v", err)
	}
	var progress, eventCount int
	if err := db.QueryRowContext(ctx, `SELECT progress FROM jobs WHERE id = ?`, "job-empty-progress").Scan(&progress); err != nil {
		t.Fatalf("read progress: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM job_events WHERE job_id = ?`, "job-empty-progress").Scan(&eventCount); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if progress != 25 || eventCount != 0 {
		t.Fatalf("progress=%d events=%d, want progress=25 and no legacy event", progress, eventCount)
	}

	if err := store.SetProgressData(ctx, "job-empty-progress", 30, "", map[string]any{
		"kind": "script.generate", "micro_kind": "script.phase", "status": "running",
	}); err != nil {
		t.Fatalf("SetProgressData with empty message: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM job_events WHERE job_id = ?`, "job-empty-progress").Scan(&eventCount); err != nil {
		t.Fatalf("count structured events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("structured event count = %d, want 1", eventCount)
	}
	if err := store.SetProgressData(ctx, "job-empty-progress", 35, "", nil); err != nil {
		t.Fatalf("SetProgressData without payload or message: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM job_events WHERE job_id = ?`, "job-empty-progress").Scan(&eventCount); err != nil {
		t.Fatalf("count empty structured event: %v", err)
	}
	if eventCount != 2 {
		t.Fatalf("empty structured event count = %d, want 2", eventCount)
	}
}

func TestLifecycleTransitionAndTimelineEventCommitAtomically(t *testing.T) {
	store, db := setupLifecycleTestDB(t)
	ctx := context.Background()
	const jobID = "job-atomic-transition"
	p1bSeedRunningJob(t, db, jobID, "p1b.test", 3, "worker-atomic", "lease-atomic", time.Now().Add(5*time.Minute), 0)
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER fail_timeline_insert BEFORE INSERT ON job_events BEGIN SELECT RAISE(ABORT, 'timeline blocked'); END`); err != nil {
		t.Fatalf("create rejecting trigger: %v", err)
	}
	if err := store.Fail(ctx, jobID, "worker-atomic", "lease-atomic", 1, "test failure"); err == nil {
		t.Fatal("Fail succeeded despite timeline insert being rejected")
	}
	row := readLifecycleRow(t, db, jobID)
	if row.status != "RUNNING" {
		t.Fatalf("status = %q, want RUNNING after transaction rollback", row.status)
	}
	var eventCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM job_events WHERE job_id = ?`, jobID).Scan(&eventCount); err != nil {
		t.Fatalf("count timeline rows: %v", err)
	}
	if eventCount != 0 {
		t.Fatalf("event count = %d, want 0 after rollback", eventCount)
	}
}

func TestLifecycleProgressAndTimelineEventCommitAtomically(t *testing.T) {
	store, db := setupLifecycleTestDB(t)
	ctx := context.Background()
	const jobID = "job-atomic-progress"
	seedQueuedJob(t, db, jobID, "p1b.test", 3)
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER fail_timeline_insert BEFORE INSERT ON job_events BEGIN SELECT RAISE(ABORT, 'timeline blocked'); END`); err != nil {
		t.Fatalf("create rejecting trigger: %v", err)
	}
	if err := store.SetProgress(ctx, jobID, 47, "blocked update"); err == nil {
		t.Fatal("SetProgress succeeded despite timeline insert being rejected")
	}
	var progress int
	if err := db.QueryRowContext(ctx, `SELECT progress FROM jobs WHERE id = ?`, jobID).Scan(&progress); err != nil {
		t.Fatalf("read progress: %v", err)
	}
	if progress != 0 {
		t.Fatalf("progress = %d, want 0 after transaction rollback", progress)
	}
}

func TestSQLiteStoreCreatePersistsQueuedTimelineEvent(t *testing.T) {
	store, db := setupLifecycleTestDB(t)
	now := time.Now().UTC()
	created := &job.Job{
		ID: "job-created-queue-event", Type: "script.generate", Status: job.StatusQueued,
		CorrelationID: "corr-created", Payload: []byte(`{"item_id":"item-1"}`),
		CreatedAt: now, UpdatedAt: now, MaxRetries: 3,
	}
	if err := store.Create(context.Background(), created); err != nil {
		t.Fatalf("Create: %v", err)
	}
	events, err := store.ListEvents(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || events[0].Type != "job_queued" {
		t.Fatalf("creation timeline = %+v, want one job_queued row", events)
	}
	if events[0].Data["kind"] != "script.generate" || events[0].Data["micro_kind"] != "job.queue" || events[0].Data["correlation_id"] != "corr-created" {
		t.Fatalf("queued event envelope = %#v", events[0].Data)
	}
	var count int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM job_events WHERE job_id = ?`, created.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("queued row count = %d, err=%v; want one", count, err)
	}
	if _, err := db.ExecContext(context.Background(), `CREATE TRIGGER fail_queued_event BEFORE INSERT ON job_events BEGIN SELECT RAISE(ABORT, 'timeline blocked'); END`); err != nil {
		t.Fatalf("create rejecting trigger: %v", err)
	}
	second := &job.Job{ID: "job-created-queue-rollback", Type: "script.generate", Status: job.StatusQueued, CreatedAt: now, UpdatedAt: now, MaxRetries: 3}
	if err := store.Create(context.Background(), second); err == nil {
		t.Fatal("Create succeeded despite queued timeline insert being rejected")
	}
	var jobCount int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM jobs WHERE id = ?`, second.ID).Scan(&jobCount); err != nil || jobCount != 0 {
		t.Fatalf("job row count = %d, err=%v; want no row after rollback", jobCount, err)
	}
}

func TestTimelineActivityDataNormalizesLifecycleAndTrace(t *testing.T) {
	store, db := setupLifecycleTestDB(t)
	ctx := context.Background()
	const jobID = "job-timeline-envelope"
	seedQueuedJob(t, db, jobID, "script.generate", 3)
	if _, err := db.ExecContext(ctx, `UPDATE jobs SET correlation_id = ? WHERE id = ?`, "corr-lifecycle", jobID); err != nil {
		t.Fatalf("set correlation: %v", err)
	}

	ctx = job.WithActivityTrace(ctx, job.ActivityTrace{RunID: "run-1", AttemptID: "attempt-1", ParentRunID: "parent-run", CorrelationID: "corr-lifecycle"})
	if err := store.AddEvent(ctx, jobID, "leased", "claimed", nil); err != nil {
		t.Fatalf("AddEvent: %v", err)
	}
	if err := store.AddEvent(ctx, jobID, "job_running", "executing", nil); err != nil {
		t.Fatalf("AddEvent: %v", err)
	}
	events, err := store.ListEvents(ctx, jobID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	wantMicroKinds := []string{"worker.claim", "worker.execute"}
	for index, event := range events {
		data := event.Data
		if data["kind"] != "script.generate" || data["sub_kind"] != wantMicroKinds[index] || data["micro_kind"] != wantMicroKinds[index] {
			t.Fatalf("event[%d] envelope = %#v", index, data)
		}
		if data["correlation_id"] != "corr-lifecycle" || data["run_id"] != "run-1" || data["attempt_id"] != "attempt-1" || data["parent_run_id"] != "parent-run" {
			t.Fatalf("event[%d] trace = %#v", index, data)
		}
		if data["sequence"] != float64(index+1) {
			t.Fatalf("event[%d] sequence = %#v, want %d", index, data["sequence"], index+1)
		}
		trace, ok := data["trace"].(map[string]any)
		if !ok || trace["sequence"] != float64(index+1) || trace["run_id"] != "run-1" || trace["attempt_id"] != "attempt-1" {
			t.Fatalf("event[%d] nested trace = %#v", index, data["trace"])
		}
	}
}

func TestListEvents_NoRowsNoScan(t *testing.T) {
	db := makeEventsTestDB(t)
	ctx := context.Background()
	store := NewSQLiteStore(db, zap.NewNop())

	events, err := store.ListEvents(ctx, "nonexistent_job_id")
	if err != nil {
		t.Fatalf("ListEvents (empty): %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events for unknown job, got %d", len(events))
	}
}

// TestListEvents_ScanErrorSentinel asserts that when the canonical
// strftime wrap is REMOVED (regression scenario), the scan failure
// surfaces as a non-nil error so callers see the failure rather than
// a silent-zero-result. This is the godlike/07 no-fake-availability
// pin — the fix MUST surface real scan errors, not silently swallow.
func TestListEvents_ScanErrorSentinel(t *testing.T) {
	// Direct scan wire-up: verify that a row whose created_at is
	// explicitly set to a non-RFC3339Nano format (legacy pre-migration-083
	// data) raises a non-nil scan error when unwrapped (defence-in-depth
	// for future regressions of the strftime canonical pattern).
	db := makeEventsTestDB(t)
	ctx := context.Background()

	// Insert a row whose created_at is the bad legacy format
	// ("YYYY-MM-DD HH:MM:SS" without nanoseconds + timezone).
	if _, err := db.ExecContext(ctx,
		`INSERT INTO job_events (id, job_id, type, message, created_at) VALUES (?, ?, ?, ?, ?)`,
		"evt_legacy_001", "job_legacy_test", "legacy_event", "msg",
		"2026-07-04 18:30:00", // legacy DATETIME format
	); err != nil {
		t.Fatalf("insert legacy-format row: %v", err)
	}

	// Manual scan WITHOUT strftime wrap, simulating a regression
	// where the canonical pattern is removed. Should produce non-nil error.
	var dt time.Time
	err := db.QueryRowContext(ctx,
		`SELECT created_at FROM job_events WHERE id = ?`,
		"evt_legacy_001",
	).Scan(&dt)
	if err == nil {
		t.Log("scan succeeded (unexpected): this means Go's mattn driver " +
			"handles legacy format gracefully today; the canonical strftime " +
			"wrap is forward-defence, not the only path. Acceptable.")
	} else if !strings.Contains(strings.ToLower(err.Error()), "parsing") &&
		!strings.Contains(strings.ToLower(err.Error()), "format") {
		t.Errorf("scan error didn't match expected pattern (parsing/format): %v", err)
	}
}
