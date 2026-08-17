// Package jobregistry persists the Job Registry in SQLite.
package jobregistry

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobregistry"
	"github.com/google/uuid"
)

type Registry struct{ db *sql.DB }

func New(db *sql.DB) (*Registry, error) {
	if db == nil {
		return nil, errors.New("job registry: nil database")
	}
	return &Registry{db: db}, nil
}

var _ capregistry.Registry = (*Registry)(nil)

func (r *Registry) RecordJob(ctx context.Context, j capregistry.Job) error {
	if j.JobID == "" || j.JobType == "" {
		return errors.New("job registry: job_id and job_type are required")
	}
	hash := j.PayloadHash
	if hash == "" {
		hash = hashPayload(j.PayloadJSON)
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO jobs
		(id,type,status,project,video_name,correlation_id,payload_json,payload_hash,result_json,error,worker_id,
		 created_at,updated_at,started_at,completed_at,project_id,video_id,parent_job_id,root_job_id,host,duration_ms,git_sha,app_version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		j.JobID, j.JobType, nonEmpty(j.Status, "QUEUED"), j.ProjectID, j.VideoID, j.CorrelationID,
		nonEmpty(j.PayloadJSON, "{}"), hash, nonEmpty(j.ResultJSON, "{}"), j.ErrorMessage, j.WorkerID,
		nonEmpty(j.CreatedAt, "1970-01-01T00:00:00Z"), nonEmpty(j.CreatedAt, "1970-01-01T00:00:00Z"), nullIfEmpty(j.StartedAt), nullIfEmpty(j.CompletedAt),
		j.ProjectID, j.VideoID, j.ParentJobID, j.RootJobID, j.Host, j.DurationMS, j.GitSHA, j.AppVersion)
	if err != nil {
		return fmt.Errorf("record job %q: %w", j.JobID, err)
	}
	return r.UpdateJob(ctx, j)
}

func (r *Registry) UpdateJob(ctx context.Context, j capregistry.Job) error {
	hash := j.PayloadHash
	if hash == "" {
		hash = hashPayload(j.PayloadJSON)
	}
	_, err := r.db.ExecContext(ctx, `UPDATE jobs SET status=?, correlation_id=?, project_id=?, video_id=?, parent_job_id=?, root_job_id=?, payload_json=?, payload_hash=?, result_json=?, error=?, worker_id=?, started_at=?, completed_at=?, duration_ms=?, git_sha=?, app_version=?, updated_at=COALESCE(?, updated_at) WHERE id=?`,
		nonEmpty(j.Status, "QUEUED"), j.CorrelationID, j.ProjectID, j.VideoID, j.ParentJobID, j.RootJobID,
		nonEmpty(j.PayloadJSON, "{}"), hash, nonEmpty(j.ResultJSON, "{}"), j.ErrorMessage, j.WorkerID,
		nullIfEmpty(j.StartedAt), nullIfEmpty(j.CompletedAt), j.DurationMS, j.GitSHA, j.AppVersion, nullIfEmpty(j.CompletedAt), j.JobID)
	if err != nil {
		return fmt.Errorf("update job %q: %w", j.JobID, err)
	}
	return nil
}

// BackfillPayloadHashes recomputes and persists the canonical payload hash
// for jobs whose payload_hash column is empty. It is the one-time repair for
// rows written before the hash was computed at record time. It reuses the
// same canonical hashPayload used by RecordJob/UpdateJob, so backfilled
// values are byte-identical to what a fresh RecordJob would have written.
// Returns the number of rows updated. The UPDATE is guarded by
// payload_hash = ” so re-running converges (idempotent).
func (r *Registry) BackfillPayloadHashes(ctx context.Context, limit int) (int, error) {
	if r == nil || r.db == nil {
		return 0, errors.New("job registry: nil database")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	query := `SELECT id, payload_json FROM jobs WHERE payload_hash = '' ORDER BY created_at ASC`
	var rows *sql.Rows
	var err error
	if limit > 0 {
		query += ` LIMIT ?`
		rows, err = r.db.QueryContext(ctx, query, limit)
	} else {
		rows, err = r.db.QueryContext(ctx, query)
	}
	if err != nil {
		return 0, fmt.Errorf("job registry: scan empty payload_hash jobs: %w", err)
	}
	defer rows.Close()

	type candidate struct {
		id      string
		payload sql.NullString
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.payload); err != nil {
			return 0, fmt.Errorf("job registry: scan candidate: %w", err)
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("job registry: iterate candidates: %w", err)
	}

	updated := 0
	for _, c := range candidates {
		hash := hashPayload(c.payload.String)
		res, err := r.db.ExecContext(ctx, `UPDATE jobs SET payload_hash = ? WHERE id = ? AND payload_hash = ''`, hash, c.id)
		if err != nil {
			return updated, fmt.Errorf("job registry: update payload_hash for %q: %w", c.id, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			updated++
		}
	}
	return updated, nil
}

func (r *Registry) RecordStep(ctx context.Context, s capregistry.Step) error {
	if s.StepID == "" || s.JobID == "" || s.StepName == "" {
		return errors.New("job registry: step identity is required")
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO job_steps (step_id,job_id,step_name,step_type,status,started_at,completed_at,duration_ms,input_count,output_count,input_bytes,output_bytes,metrics_json,error_code,error_message,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(step_id) DO UPDATE SET status=excluded.status, completed_at=excluded.completed_at, duration_ms=excluded.duration_ms, output_count=excluded.output_count, output_bytes=excluded.output_bytes, metrics_json=excluded.metrics_json, error_code=excluded.error_code, error_message=excluded.error_message`,
		s.StepID, s.JobID, s.StepName, s.StepType, s.Status, nullIfEmpty(s.StartedAt), nullIfEmpty(s.CompletedAt), s.DurationMS, s.InputCount, s.OutputCount, s.InputBytes, s.OutputBytes, nonEmpty(s.MetricsJSON, "{}"), s.ErrorCode, s.ErrorMessage, nonEmpty(s.CreatedAt, "1970-01-01T00:00:00Z"))
	if err != nil {
		return fmt.Errorf("record step %q: %w", s.StepID, err)
	}
	return nil
}

func (r *Registry) RecordMetric(ctx context.Context, m capregistry.Metric) error {
	if m.MetricID == "" {
		m.MetricID = uuid.NewString()
	}
	if m.JobID == "" || m.Name == "" {
		return errors.New("job registry: metric job_id and name are required")
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO job_registry_metrics (metric_id,job_id,step_id,metric_name,metric_value,unit,created_at) VALUES (?,?,?,?,?,?,?) ON CONFLICT(metric_id) DO NOTHING`, m.MetricID, m.JobID, nullIfEmpty(m.StepID), m.Name, m.Value, m.Unit, nonEmpty(m.CreatedAt, "1970-01-01T00:00:00Z"))
	if err != nil {
		return fmt.Errorf("record metric %q: %w", m.MetricID, err)
	}
	return nil
}

func (r *Registry) RelateAsset(ctx context.Context, a capregistry.AssetRelation) error {
	if a.JobID == "" || a.AssetID == "" || a.Relation == "" {
		return errors.New("job registry: asset relation identity is required")
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO job_asset_relations (job_id,asset_id,relation,step_id,ordinal,created_at) VALUES (?,?,?,?,?,?) ON CONFLICT(job_id,asset_id,relation,step_id) DO UPDATE SET ordinal=excluded.ordinal`, a.JobID, a.AssetID, a.Relation, a.StepID, a.Ordinal, nonEmpty(a.CreatedAt, "1970-01-01T00:00:00Z"))
	if err != nil {
		return fmt.Errorf("relate job %q to asset %q: %w", a.JobID, a.AssetID, err)
	}
	return nil
}

func (r *Registry) AppendEvent(ctx context.Context, e capregistry.Event) (int64, error) {
	if e.EventID == "" {
		e.EventID = uuid.NewString()
	}
	if e.JobID == "" || e.EventType == "" {
		return 0, errors.New("job registry: event identity is required")
	}
	res, err := r.db.ExecContext(ctx, `INSERT INTO job_registry_events (event_id,job_id,event_type,payload_json,created_at) VALUES (?,?,?,?,?) ON CONFLICT(event_id) DO NOTHING`, e.EventID, e.JobID, e.EventType, nonEmpty(e.PayloadJSON, "{}"), nonEmpty(e.CreatedAt, "1970-01-01T00:00:00Z"))
	if err != nil {
		return 0, fmt.Errorf("append job event %q: %w", e.EventID, err)
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		var seq int64
		if err := r.db.QueryRowContext(ctx, `SELECT seq FROM job_registry_events WHERE event_id=?`, e.EventID).Scan(&seq); err != nil {
			return 0, fmt.Errorf("append job event %q: read existing event: %w", e.EventID, err)
		}
		return seq, nil
	}
	return res.LastInsertId()
}

func (r *Registry) Stats(ctx context.Context, from, to string) (capregistry.Stats, error) {
	stats := capregistry.Stats{From: from, To: to}
	args := []any{from, to}
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE WHEN UPPER(status)='SUCCEEDED' THEN 1 ELSE 0 END),0), COALESCE(SUM(CASE WHEN UPPER(status)='FAILED' THEN 1 ELSE 0 END),0), COALESCE(AVG(CASE WHEN started_at IS NOT NULL AND (completed_at IS NOT NULL OR updated_at IS NOT NULL) THEN COALESCE(NULLIF(duration_ms,0), (julianday(COALESCE(completed_at,updated_at))-julianday(started_at))*86400000.0) END),0) FROM jobs WHERE created_at >= ? AND created_at < ?`, args...).Scan(&stats.Jobs, &stats.Successful, &stats.Failed, &stats.AvgPipelineMS); err != nil {
		return stats, fmt.Errorf("job stats totals: %w", err)
	}
	queries := []struct {
		relation string
		target   *int
	}{{"GENERATED", &stats.ScriptsGenerated}, {"DOWNLOADED", &stats.ClipsDownloaded}, {"GENERATED_IMAGE", &stats.ImagesGenerated}, {"GENERATED_VOICEOVER", &stats.VoiceoversGenerated}, {"RENDERED", &stats.VideosRendered}}
	for _, q := range queries {
		if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM job_asset_relations WHERE relation=? AND created_at >= ? AND created_at < ?`, q.relation, from, to).Scan(q.target); err != nil {
			return stats, fmt.Errorf("job stats relation %s: %w", q.relation, err)
		}
	}
	// Steps are persisted as COMPLETED (never SUCCEEDED) by the worker's
	// statusForStep projection, so the slowest-step aggregate must accept
	// both spellings or it silently under-reports every script runner step.
	rows, err := r.db.QueryContext(ctx, `SELECT step_name, COUNT(*), AVG(duration_ms) FROM job_steps WHERE UPPER(status) IN ('SUCCEEDED','COMPLETED') AND started_at >= ? AND started_at < ? GROUP BY step_name ORDER BY AVG(duration_ms) DESC`, from, to)
	if err != nil {
		return stats, fmt.Errorf("job stats steps: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var s capregistry.StepSummary
		if err := rows.Scan(&s.StepName, &s.Runs, &s.AvgDurationMS); err != nil {
			return stats, err
		}
		stats.SlowestSteps = append(stats.SlowestSteps, s)
	}
	if err := rows.Err(); err != nil {
		return stats, err
	}
	return stats, nil
}

func hashPayload(raw string) string {
	if strings.TrimSpace(raw) == "" {
		raw = "{}"
	}
	var v any
	if json.Unmarshal([]byte(raw), &v) == nil {
		if b, err := json.Marshal(v); err == nil {
			raw = string(b)
		}
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
func nonEmpty(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
func nullIfEmpty(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}
