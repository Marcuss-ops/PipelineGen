package processmetrics_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"

	processmetrics "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/processmetrics"
)

const schema = `
CREATE TABLE process_phase_metrics (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    process_type TEXT NOT NULL,
    job_id TEXT NOT NULL,
    parent_job_id TEXT NOT NULL DEFAULT '',
    phase TEXT NOT NULL,
    language TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT '',
    started_at TEXT NOT NULL,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    queue_wait_ms INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL,
    error_code TEXT NOT NULL DEFAULT '',
    items_in INTEGER NOT NULL DEFAULT 0,
    items_out INTEGER NOT NULL DEFAULT 0,
    bytes_in INTEGER NOT NULL DEFAULT 0,
    bytes_out INTEGER NOT NULL DEFAULT 0,
    retry_count INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    details_json TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX idx_process_phase_metrics_job
    ON process_phase_metrics(job_id, started_at DESC);
CREATE INDEX idx_process_phase_metrics_type_phase
    ON process_phase_metrics(process_type, phase, created_at DESC);
CREATE INDEX idx_process_phase_metrics_parent_job
    ON process_phase_metrics(parent_job_id, started_at DESC)
    WHERE parent_job_id != '';
`

func newRepository(t *testing.T) *processmetrics.SQLiteRepository {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(schema)
	require.NoError(t, err)
	return processmetrics.NewSQLiteRepository(db)
}

func sampleMetric() *processmetrics.Metric {
	started := time.Date(2026, 7, 31, 12, 0, 0, 123456789, time.UTC)
	return &processmetrics.Metric{
		ProcessType: "stock",
		JobID:       "job-123",
		ParentJobID: "run-123",
		Phase:       "stock.youtube_download",
		Language:    "",
		Provider:    "youtube",
		StartedAt:   started,
		DurationMs:  48200,
		QueueWaitMs: 3000,
		Status:      "success",
		ItemsIn:     3,
		ItemsOut:    3,
		BytesOut:    285000000,
		RetryCount:  1,
		Details: map[string]any{
			"videos_found":            float64(3),
			"download_bytes":          float64(285000000),
			"output_duration_seconds": float64(15.5),
			"segments_completed":      float64(3),
		},
	}
}

func TestRepository_InsertAndGetByIDRoundTrip(t *testing.T) {
	r := newRepository(t)
	want := sampleMetric()

	id, err := r.Insert(context.Background(), want)
	require.NoError(t, err)
	require.Positive(t, id)
	require.Equal(t, id, want.ID)

	got, err := r.GetByID(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, want.ID, got.ID)
	require.Equal(t, want.ProcessType, got.ProcessType)
	require.Equal(t, want.JobID, got.JobID)
	require.Equal(t, want.ParentJobID, got.ParentJobID)
	require.Equal(t, want.Phase, got.Phase)
	require.Equal(t, want.Provider, got.Provider)
	require.Equal(t, want.DurationMs, got.DurationMs)
	require.Equal(t, want.QueueWaitMs, got.QueueWaitMs)
	require.Equal(t, want.ItemsIn, got.ItemsIn)
	require.Equal(t, want.ItemsOut, got.ItemsOut)
	require.Equal(t, want.BytesOut, got.BytesOut)
	require.Equal(t, want.RetryCount, got.RetryCount)
	require.Equal(t, want.StartedAt, got.StartedAt)
	require.Equal(t, want.Details, got.Details)
	require.NotZero(t, got.CreatedAt)
}

func TestRepository_RejectsInvalidMetric(t *testing.T) {
	r := newRepository(t)

	cases := []struct {
		name   string
		mutate func(*processmetrics.Metric)
	}{
		{"missing process type", func(m *processmetrics.Metric) { m.ProcessType = "" }},
		{"missing job", func(m *processmetrics.Metric) { m.JobID = "" }},
		{"missing phase", func(m *processmetrics.Metric) { m.Phase = "" }},
		{"missing started at", func(m *processmetrics.Metric) { m.StartedAt = time.Time{} }},
		{"missing status", func(m *processmetrics.Metric) { m.Status = "" }},
		{"negative duration", func(m *processmetrics.Metric) { m.DurationMs = -1 }},
		{"negative retry", func(m *processmetrics.Metric) { m.RetryCount = -1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			metric := sampleMetric()
			tc.mutate(metric)
			_, err := r.Insert(context.Background(), metric)
			require.Error(t, err)
		})
	}
}

func TestRepository_UpdateAndListByJob(t *testing.T) {
	r := newRepository(t)
	metric := sampleMetric()
	id, err := r.Insert(context.Background(), metric)
	require.NoError(t, err)

	metric.ID = id
	metric.Status = "failure"
	metric.ErrorCode = "download_timeout"
	metric.DurationMs = 90000
	require.NoError(t, r.Update(context.Background(), metric))

	got, err := r.GetByID(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, "failure", got.Status)
	require.Equal(t, "download_timeout", got.ErrorCode)
	require.Equal(t, int64(90000), got.DurationMs)

	rows, err := r.ListByJob(context.Background(), metric.JobID, 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, id, rows[0].ID)
}

func TestRepository_ListByParentJobOrdersNewestFirstAndAppliesLimit(t *testing.T) {
	r := newRepository(t)
	base := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		metric := sampleMetric()
		metric.ID = 0
		metric.JobID = "job-" + string(rune('1'+i))
		metric.StartedAt = base.Add(time.Duration(i) * time.Minute)
		_, err := r.Insert(context.Background(), metric)
		require.NoError(t, err)
	}

	rows, err := r.ListByParentJob(context.Background(), "run-123", 2)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "job-3", rows[0].JobID)
	require.Equal(t, "job-2", rows[1].JobID)
}

func TestRepository_GetByIDNotFound(t *testing.T) {
	r := newRepository(t)
	_, err := r.GetByID(context.Background(), 999)
	require.Error(t, err)
	require.True(t, errors.Is(err, sql.ErrNoRows))
}

func TestRepository_UpdateNotFound(t *testing.T) {
	r := newRepository(t)
	metric := sampleMetric()
	metric.ID = 999
	err := r.Update(context.Background(), metric)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}
