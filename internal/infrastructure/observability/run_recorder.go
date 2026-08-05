package observability

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"time"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"go.uber.org/zap"
)

type SQLiteRecorder struct {
	db       *sql.DB
	log      *zap.Logger
	mu       sync.Mutex
	degraded map[string]bool
}

func NewSQLiteRecorder(db *sql.DB) *SQLiteRecorder {
	return NewSQLiteRecorderWithLogger(db, zap.NewNop())
}
func NewSQLiteRecorderWithLogger(db *sql.DB, log *zap.Logger) *SQLiteRecorder {
	if log == nil {
		log = zap.NewNop()
	}
	return &SQLiteRecorder{db: db, log: log, degraded: map[string]bool{}}
}

func (r *SQLiteRecorder) StartReport(ctx context.Context, p *kernobs.RunReport) error {
	if err := validateRun(p); err != nil {
		return r.fail(reportID(p), "start_report", err)
	}
	if r == nil || r.db == nil {
		return r.fail(p.RunID, "start_report", errors.New("nil observability database"))
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now().UTC()
	started := p.StartedAt
	if started.IsZero() {
		started = now
	}
	created := p.CreatedAt
	if created.IsZero() {
		created = started
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return r.fail(p.RunID, "start_begin", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO job_attempts (attempt_id,job_id,run_id,attempt_number,worker_id,lease_id,status,started_at,lease_expires_at,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(attempt_id) DO NOTHING`, p.AttemptID, p.JobID, p.RunID, p.Counters.Retries+1, nullable(p.WorkerID), nullable(p.LeaseID), p.Status, timeValue(started), nullableTime(p.LeaseExpiresAt), timeValue(created), timeValue(now))
	if err != nil {
		return r.fail(p.RunID, "start_attempt", err)
	}
	if err := ensureAttemptIdentity(ctx, tx, p); err != nil {
		return r.fail(p.RunID, "start_attempt_identity", err)
	}
	body, err := p.JSON()
	if err != nil {
		return r.fail(p.RunID, "marshal_start", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO run_observability (run_id,job_id,job_type,attempt_id,parent_run_id,worker_id,lease_id,lease_expires_at,status,created_at,started_at,queue_wait_ms,report_json,observability_degraded,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(run_id) DO NOTHING`, p.RunID, p.JobID, p.JobType, p.AttemptID, nullable(p.ParentRunID), nullable(p.WorkerID), nullable(p.LeaseID), nullableTime(p.LeaseExpiresAt), p.Status, timeValue(created), timeValue(started), p.QueueWaitMs, string(body), boolInt(p.ObservabilityDegraded), timeValue(now))
	if err != nil {
		return r.fail(p.RunID, "start_run", err)
	}
	if err := ensureRunIdentity(ctx, tx, p); err != nil {
		return r.fail(p.RunID, "start_run_identity", err)
	}
	if err = tx.Commit(); err != nil {
		return r.fail(p.RunID, "start_commit", err)
	}
	return nil
}

func ensureStageIdentity(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, runID, observationID string) error {
	var storedRunID string
	if err := db.QueryRowContext(ctx, `SELECT run_id FROM run_stage_observations WHERE observation_id=?`, observationID).Scan(&storedRunID); err != nil {
		return err
	}
	if storedRunID != runID {
		return errors.New("stage observation identity conflict")
	}
	return nil
}

func ensureOperationIdentity(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, runID, observationID string) error {
	var storedRunID string
	if err := db.QueryRowContext(ctx, `SELECT run_id FROM run_operation_observations WHERE observation_id=?`, observationID).Scan(&storedRunID); err != nil {
		return err
	}
	if storedRunID != runID {
		return errors.New("operation observation identity conflict")
	}
	return nil
}

func ensureArtifactIdentity(ctx context.Context, tx *sql.Tx, runID, observationID string) error {
	var storedRunID string
	if err := tx.QueryRowContext(ctx, `SELECT run_id FROM run_artifact_observations WHERE observation_id=?`, observationID).Scan(&storedRunID); err != nil {
		return err
	}
	if storedRunID != runID {
		return errors.New("artifact observation identity conflict")
	}
	return nil
}

func ensureAttemptIdentity(ctx context.Context, tx *sql.Tx, p *kernobs.RunReport) error {
	var jobID, runID string
	if err := tx.QueryRowContext(ctx, `SELECT job_id,run_id FROM job_attempts WHERE attempt_id=?`, p.AttemptID).Scan(&jobID, &runID); err != nil {
		return err
	}
	if jobID != p.JobID || runID != p.RunID {
		return errors.New("attempt identity conflict")
	}
	return nil
}

func ensureRunIdentity(ctx context.Context, tx *sql.Tx, p *kernobs.RunReport) error {
	var jobID, attemptID string
	if err := tx.QueryRowContext(ctx, `SELECT job_id,attempt_id FROM run_observability WHERE run_id=?`, p.RunID).Scan(&jobID, &attemptID); err != nil {
		return err
	}
	if jobID != p.JobID || attemptID != p.AttemptID {
		return errors.New("run identity conflict")
	}
	return nil
}

func (r *SQLiteRecorder) AppendStage(ctx context.Context, runID string, s kernobs.StageReport) error {
	if r == nil || r.db == nil {
		return r.fail(runID, "append_stage", errors.New("nil observability database"))
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if runID == "" || s.ObservationID == "" || s.Name == "" {
		return r.fail(runID, "append_stage", errors.New("run_id, observation_id and name are required"))
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO run_stage_observations (observation_id,run_id,name,status,duration_ms,attempts,cache_status,error_code,items_input,items_completed,items_failed,bytes_processed) VALUES (?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(observation_id) DO NOTHING`, s.ObservationID, runID, s.Name, s.Status, s.DurationMs, s.Attempts, nullable(s.CacheStatus), nullable(s.ErrorCode), s.ItemsInput, s.ItemsCompleted, s.ItemsFailed, s.BytesProcessed)
	if err != nil {
		return r.fail(runID, "append_stage", err)
	}
	if err := ensureStageIdentity(ctx, r.db, runID, s.ObservationID); err != nil {
		return r.fail(runID, "append_stage_identity", err)
	}
	return nil
}
func (r *SQLiteRecorder) AppendOperation(ctx context.Context, runID string, o kernobs.OperationReport) error {
	if r == nil || r.db == nil {
		return r.fail(runID, "append_operation", errors.New("nil observability database"))
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if runID == "" || o.ObservationID == "" || o.Operation == "" {
		return r.fail(runID, "append_operation", errors.New("run_id, observation_id and operation are required"))
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO run_operation_observations (observation_id,run_id,stage,component,operation,provider,status,duration_ms,attempts,items,bytes,cache_status,error_code) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(observation_id) DO NOTHING`, o.ObservationID, runID, o.Stage, o.Component, o.Operation, nullable(o.Provider), o.Status, o.DurationMs, o.Attempts, o.Items, o.Bytes, nullable(o.CacheStatus), nullable(o.ErrorCode))
	if err != nil {
		return r.fail(runID, "append_operation", err)
	}
	if err := ensureOperationIdentity(ctx, r.db, runID, o.ObservationID); err != nil {
		return r.fail(runID, "append_operation_identity", err)
	}
	return nil
}

func (r *SQLiteRecorder) SaveReport(ctx context.Context, p *kernobs.RunReport) error {
	if err := validateRun(p); err != nil {
		return r.fail(reportID(p), "save_report", err)
	}
	if r == nil || r.db == nil {
		return r.fail(p.RunID, "save_report", errors.New("nil observability database"))
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if r.isDegraded(p.RunID) {
		c := cloneReport(p)
		c.ObservabilityDegraded = true
		p = c
	}
	body, err := p.JSON()
	if err != nil {
		return r.fail(p.RunID, "marshal_save", err)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return r.fail(p.RunID, "save_begin", err)
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	res, err := tx.ExecContext(ctx, `UPDATE run_observability SET status=?,finished_at=?,queue_wait_ms=?,wall_time_ms=?,blocked_ms=?,accumulated_operation_ms=?,error_code=?,error=?,counters_json=?,children_json=?,report_json=?,observability_degraded=?,updated_at=? WHERE run_id=?`, p.Status, nullableTime(p.FinishedAt), p.QueueWaitMs, p.WallTimeMs, p.BlockedMs, p.AccumulatedOperationMs, nullable(p.ErrorCode), nullable(p.Error), jsonValue(p.Counters), jsonValue(p.Children), string(body), boolInt(p.ObservabilityDegraded), timeValue(now), p.RunID)
	if err != nil {
		return r.fail(p.RunID, "finish_run", err)
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		if err == nil {
			err = errors.New("run_observability row not found")
		}
		return r.fail(p.RunID, "finish_rows", err)
	}
	for _, a := range p.Artifacts {
		if a.ObservationID == "" {
			return r.fail(p.RunID, "save_artifact", errors.New("artifact observation_id is required"))
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO run_artifact_observations (observation_id,run_id,kind,ref,url,stage,bytes,reused) VALUES (?,?,?,?,?,?,?,?) ON CONFLICT(observation_id) DO NOTHING`, a.ObservationID, p.RunID, a.Kind, nullable(a.Ref), nullable(a.URL), nullable(a.Stage), a.Bytes, boolInt(a.Reused)); err != nil {
			return r.fail(p.RunID, "save_artifact", err)
		}
		if err := ensureArtifactIdentity(ctx, tx, p.RunID, a.ObservationID); err != nil {
			return r.fail(p.RunID, "save_artifact_identity", err)
		}
	}
	res, err = tx.ExecContext(ctx, `UPDATE job_attempts SET status=?,finished_at=?,error_code=?,error=?,updated_at=? WHERE attempt_id=?`, p.Status, nullableTime(p.FinishedAt), nullable(p.ErrorCode), nullable(p.Error), timeValue(now), p.AttemptID)
	if err != nil {
		return r.fail(p.RunID, "finish_attempt", err)
	}
	n, err = res.RowsAffected()
	if err != nil || n != 1 {
		if err == nil {
			err = errors.New("job_attempts row not found")
		}
		return r.fail(p.RunID, "finish_attempt_rows", err)
	}
	if err := upsertChildAndRefreshParent(ctx, tx, p); err != nil {
		return r.fail(p.RunID, "save_child", err)
	}
	if err = tx.Commit(); err != nil {
		return r.fail(p.RunID, "save_commit", err)
	}
	return nil
}

// RecordChild persists a child lifecycle snapshot. It is idempotent by the
// canonical (parent_run_id, child_job_id) key, so enqueue deduplication and
// terminal retries cannot inflate the parent summary.
func (r *SQLiteRecorder) RecordChild(ctx context.Context, child *kernobs.RunReport) error {
	if child == nil || child.ParentRunID == "" || child.JobID == "" {
		return r.fail(reportID(child), "record_child", errors.New("parent_run_id and child job_id are required"))
	}
	if r == nil || r.db == nil {
		return r.fail(reportID(child), "record_child", errors.New("nil observability database"))
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return r.fail(child.ParentRunID, "child_begin", err)
	}
	defer tx.Rollback()
	if err := upsertChildAndRefreshParent(ctx, tx, child); err != nil {
		return r.fail(child.ParentRunID, "record_child", err)
	}
	if err := tx.Commit(); err != nil {
		return r.fail(child.ParentRunID, "child_commit", err)
	}
	return nil
}

func upsertChildAndRefreshParent(ctx context.Context, tx *sql.Tx, child *kernobs.RunReport) error {
	if child == nil || child.ParentRunID == "" || child.JobID == "" {
		return nil
	}
	now := time.Now().UTC()
	_, err := tx.ExecContext(ctx, `INSERT INTO run_child_observations (parent_run_id,child_job_id,child_run_id,status,wall_time_ms,updated_at) VALUES (?,?,?,?,?,?) ON CONFLICT(parent_run_id,child_job_id) DO UPDATE SET child_run_id=excluded.child_run_id,status=excluded.status,wall_time_ms=excluded.wall_time_ms,updated_at=excluded.updated_at`, child.ParentRunID, child.JobID, child.RunID, child.Status, child.WallTimeMs, timeValue(now))
	if err != nil {
		return err
	}
	return refreshParentSummary(ctx, tx, child.ParentRunID)
}

func refreshParentSummary(ctx context.Context, tx *sql.Tx, parentRunID string) error {
	if parentRunID == "" {
		return nil
	}
	now := time.Now().UTC()
	var summary kernobs.ChildrenSummary
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE WHEN status = 'SUCCEEDED' THEN 1 ELSE 0 END),0), COALESCE(SUM(CASE WHEN status IN ('FAILED','CANCELLED','ABANDONED') THEN 1 ELSE 0 END),0), COALESCE(SUM(wall_time_ms),0) FROM run_child_observations WHERE parent_run_id=?`, parentRunID).Scan(&summary.Requested, &summary.Completed, &summary.Failed, &summary.AccumulatedChildMs); err != nil {
		return err
	}
	var parentJSON string
	if err := tx.QueryRowContext(ctx, `SELECT report_json FROM run_observability WHERE run_id=?`, parentRunID).Scan(&parentJSON); err != nil {
		return err
	}
	var parent kernobs.RunReport
	if err := json.Unmarshal([]byte(parentJSON), &parent); err != nil {
		return err
	}
	parent.Children = &summary
	updated, err := parent.JSON()
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE run_observability SET children_json=?,report_json=?,updated_at=? WHERE run_id=?`, jsonValue(&summary), string(updated), timeValue(now), parentRunID)
	return err
}

func (r *SQLiteRecorder) RecoverAbandoned(ctx context.Context, now time.Time) (int64, error) {
	if r == nil || r.db == nil {
		return 0, r.fail("", "recover", errors.New("nil observability database"))
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	v := timeValue(now)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, r.fail("", "recover_begin", err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE run_observability SET status='ABANDONED',finished_at=?,error_code='WORKER_LOST',error=?,report_json=json_set(report_json,'$.status','ABANDONED','$.finished_at',?,'$.error_code','WORKER_LOST','$.error',?),updated_at=? WHERE status='RUNNING' AND lease_expires_at IS NOT NULL AND lease_expires_at<=?`, v, "worker lease expired before run finalization", v, "worker lease expired before run finalization", v, v)
	if err != nil {
		return 0, r.fail("", "recover_runs", err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return 0, r.fail("", "recover_rows", err)
	}
	res, err = tx.ExecContext(ctx, `UPDATE job_attempts SET status='ABANDONED',finished_at=?,error_code='WORKER_LOST',error=?,updated_at=? WHERE status='RUNNING' AND lease_expires_at IS NOT NULL AND lease_expires_at<=?`, v, "worker lease expired before run finalization", v, v)
	if err != nil {
		return 0, r.fail("", "recover_attempts", err)
	}
	attempts, err := res.RowsAffected()
	if err != nil || attempts != changed {
		if err == nil {
			err = errors.New("run and attempt recovery counts differ")
		}
		return 0, r.fail("", "recover_rows", err)
	}
	if err = tx.Commit(); err != nil {
		return 0, r.fail("", "recover_commit", err)
	}
	return changed, nil
}

func (r *SQLiteRecorder) LogRecorderFailure(_ context.Context, runID, op string, err error) {
	if r != nil {
		RecorderFailuresTotal.Inc()
		if r.log != nil {
			r.log.Error("observability recorder failure", zap.String("run_id", runID), zap.String("operation", op), zap.Error(err))
		}
	}
}
func (r *SQLiteRecorder) fail(id, op string, err error) error {
	if err == nil {
		return nil
	}
	if r != nil {
		r.mu.Lock()
		r.degraded[id] = true
		r.mu.Unlock()
		r.LogRecorderFailure(context.Background(), id, op, err)
	}
	return err
}
func (r *SQLiteRecorder) isDegraded(id string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.degraded[id]
}
func validateRun(p *kernobs.RunReport) error {
	if p == nil {
		return errors.New("nil run report")
	}
	if p.RunID == "" || p.JobID == "" || p.AttemptID == "" || p.Status == "" {
		return errors.New("run_id, job_id, attempt_id and status are required")
	}
	return nil
}
func reportID(p *kernobs.RunReport) string {
	if p == nil {
		return ""
	}
	return p.RunID
}
func cloneReport(p *kernobs.RunReport) *kernobs.RunReport {
	c := *p
	c.Stages = append([]kernobs.StageReport(nil), p.Stages...)
	c.Operations = append([]kernobs.OperationReport(nil), p.Operations...)
	c.Artifacts = append([]kernobs.ArtifactReport(nil), p.Artifacts...)
	c.Waits = append([]kernobs.WaitReport(nil), p.Waits...)
	return &c
}
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return timeValue(t)
}
func timeValue(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
func jsonValue(v any) string {
	b, e := json.Marshal(v)
	if e != nil {
		return "{}"
	}
	return string(b)
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

var _ kernobs.Recorder = (*SQLiteRecorder)(nil)
var _ kernobs.LifecycleRecorder = (*SQLiteRecorder)(nil)
var _ kernobs.RecorderFailureLogger = (*SQLiteRecorder)(nil)
var _ kernobs.AbandonedRunReconciler = (*SQLiteRecorder)(nil)
