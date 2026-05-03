package artlist

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"velox/go-master/pkg/models"
)

// artlistRunRecord represents a legacy artlist_runs record (read-only).
// The artlist_runs table is maintained for backward compatibility with legacy records.
// New runs should use the jobs table directly.
type artlistRunRecord struct {
	RunID           string
	Term            string
	RootFolderID    string
	Strategy        string
	DryRun          bool
	Status          models.JobStatus
	Found           int
	Processed       int
	Skipped         int
	Failed          int
	EstimatedSize   int
	LastProcessedAt *time.Time
	RequestJSON     string
	Error           string
	TagFolderID     string
	StartedAt       *time.Time
	EndedAt         *time.Time
	ActiveKey       string
}

func (s *Service) loadRunRecord(ctx context.Context, runID string) (*artlistRunRecord, error) {
	if s.mainDB == nil {
		return nil, fmt.Errorf("main database not configured")
	}
	row := s.mainDB.QueryRowContext(ctx, `
		SELECT run_id, term, root_folder_id, strategy, dry_run, status, found, processed, skipped, failed,
		       estimated_size, last_processed_at, request_json, error, tag_folder_id, started_at, ended_at, active_key
		FROM artlist_runs
		WHERE run_id = ?
		LIMIT 1
	`, runID)
	return scanRunRecord(row)
}

func (s *Service) findActiveRunRecord(ctx context.Context, activeKey string) (*artlistRunRecord, error) {
	if s.mainDB == nil {
		return nil, fmt.Errorf("main database not configured")
	}
	row := s.mainDB.QueryRowContext(ctx, `
		SELECT run_id, term, root_folder_id, strategy, dry_run, status, found, processed, skipped, failed,
		       estimated_size, last_processed_at, request_json, error, tag_folder_id, started_at, ended_at, active_key
		FROM artlist_runs
		WHERE active_key = ? AND status IN ('queued', 'running')
		ORDER BY started_at DESC
		LIMIT 1
		`, activeKey)
	return scanRunRecord(row)
}

func (s *Service) findRunRecordByActiveKey(ctx context.Context, activeKey string) (*artlistRunRecord, error) {
	if s.mainDB == nil {
		return nil, fmt.Errorf("main database not configured")
	}
	row := s.mainDB.QueryRowContext(ctx, `
		SELECT run_id, term, root_folder_id, strategy, dry_run, status, found, processed, skipped, failed,
		       estimated_size, last_processed_at, request_json, error, tag_folder_id, started_at, ended_at, active_key
		FROM artlist_runs
		WHERE active_key = ?
		ORDER BY started_at DESC
		LIMIT 1
		`, activeKey)
	return scanRunRecord(row)
}

func scanRunRecord(row *sql.Row) (*artlistRunRecord, error) {
	var rec artlistRunRecord
	var dryRunInt int
	var statusStr string
	var lastProcessedAt sql.NullString
	var startedAt sql.NullString
	var endedAt sql.NullString
	if err := row.Scan(
		&rec.RunID,
		&rec.Term,
		&rec.RootFolderID,
		&rec.Strategy,
		&dryRunInt,
		&statusStr,
		&rec.Found,
		&rec.Processed,
		&rec.Skipped,
		&rec.Failed,
		&rec.EstimatedSize,
		&lastProcessedAt,
		&rec.RequestJSON,
		&rec.Error,
		&rec.TagFolderID,
		&startedAt,
		&endedAt,
		&rec.ActiveKey,
	); err != nil {
		return nil, err
	}
	rec.DryRun = dryRunInt != 0
	rec.Status = models.JobStatus(statusStr)
	if lastProcessedAt.Valid {
		t, err := time.Parse(time.RFC3339, lastProcessedAt.String)
		if err == nil {
			rec.LastProcessedAt = &t
		}
	}
	if startedAt.Valid {
		t, err := time.Parse(time.RFC3339, startedAt.String)
		if err == nil {
			rec.StartedAt = &t
		}
	}
	if endedAt.Valid {
		t, err := time.Parse(time.RFC3339, endedAt.String)
		if err == nil {
			rec.EndedAt = &t
		}
	}
	return &rec, nil
}

func (s *Service) runRecordToResponse(rec *artlistRunRecord) *RunTagResponse {
	if rec == nil {
		return &RunTagResponse{OK: false, Status: "not_found", Error: "run not found"}
	}
	resp := &RunTagResponse{
		OK:            rec.Status != models.StatusFailed,
		RunID:         rec.RunID,
		Status:        string(rec.Status),
		Term:          rec.Term,
		Strategy:      rec.Strategy,
		DryRun:        rec.DryRun,
		RootFolderID:  rec.RootFolderID,
		TagFolderID:   rec.TagFolderID,
		Found:         rec.Found,
		Processed:     rec.Processed,
		Skipped:       rec.Skipped,
		Failed:        rec.Failed,
		EstimatedSize: rec.EstimatedSize,
	}
	if rec.LastProcessedAt != nil {
		s := rec.LastProcessedAt.Format(time.RFC3339)
		resp.LastProcessedAt = &s
	}
	if rec.StartedAt != nil {
		s := rec.StartedAt.Format(time.RFC3339)
		resp.StartedAt = &s
	}
	if rec.EndedAt != nil {
		s := rec.EndedAt.Format(time.RFC3339)
		resp.EndedAt = &s
	}
	return resp
}
