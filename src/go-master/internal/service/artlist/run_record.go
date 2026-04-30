package artlist

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type artlistRunRecord struct {
	RunID           string
	Term            string
	RootFolderID    string
	Strategy        string
	DryRun          bool
	Status          string
	Found           int
	Processed       int
	Skipped         int
	Failed          int
	EstimatedSize   int
	LastProcessedAt *string
	RequestJSON     string
	Error           string
	TagFolderID     string
	StartedAt       *string
	EndedAt         *string
	ActiveKey       string
}

func (s *Service) ensureRunRecord(ctx context.Context, req *RunTagRequest) (*artlistRunRecord, bool, error) {
	if s.mainDB == nil {
		return nil, false, fmt.Errorf("main database not configured")
	}
	if err := s.ensureRunSchema(ctx); err != nil {
		return nil, false, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	activeKey := runDedupKey(req.Term, req.RootFolderID, req.Strategy, req.DryRun)
	if rec, err := s.findActiveRunRecord(ctx, activeKey); err == nil && rec != nil {
		return rec, true, nil
	}

	runID := uuid.NewString()
	requestJSON, _ := json.Marshal(req)
	rec := &artlistRunRecord{
		RunID:        runID,
		Term:         req.Term,
		RootFolderID: req.RootFolderID,
		Strategy:     req.Strategy,
		DryRun:       req.DryRun,
		Status:       "running",
		RequestJSON:  string(requestJSON),
		StartedAt:    &now,
		ActiveKey:    activeKey,
	}

	_, err := s.mainDB.ExecContext(ctx, `
		INSERT INTO artlist_runs (
			run_id, term, root_folder_id, strategy, dry_run, status, active_key,
			found, processed, skipped, failed, estimated_size, request_json, error,
			tag_folder_id, started_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, rec.RunID, rec.Term, rec.RootFolderID, rec.Strategy, boolToInt(rec.DryRun), rec.Status, rec.ActiveKey,
		rec.Found, rec.Processed, rec.Skipped, rec.Failed, rec.EstimatedSize, rec.RequestJSON, rec.Error,
		rec.TagFolderID, now)
	if err != nil {
		if isUniqueConstraintErr(err) {
			if existing, findErr := s.findActiveRunRecord(ctx, activeKey); findErr == nil && existing != nil {
				return existing, true, nil
			}
		}
		return nil, false, err
	}

	return rec, false, nil
}

func (s *Service) finishRunRecord(ctx context.Context, runID, status string, resp *RunTagResponse) error {
	if s.mainDB == nil || strings.TrimSpace(runID) == "" {
		return nil
	}
	if err := s.ensureRunSchema(ctx); err != nil {
		return err
	}

	endedAt := time.Now().UTC().Format(time.RFC3339)
	lastProcessedAt := ""
	if resp != nil && resp.LastProcessedAt != nil {
		lastProcessedAt = *resp.LastProcessedAt
	}

	_, err := s.mainDB.ExecContext(ctx, `
		UPDATE artlist_runs
		SET status = ?,
			found = ?,
			processed = ?,
			skipped = ?,
			failed = ?,
			estimated_size = ?,
			last_processed_at = ?,
			error = ?,
			tag_folder_id = ?,
			ended_at = ?,
			active_key = ''
		WHERE run_id = ?
	`, status, resp.Found, resp.Processed, resp.Skipped, resp.Failed, resp.EstimatedSize, nullString(lastProcessedAt), resp.Error, resp.TagFolderID, endedAt, runID)
	return err
}

func (s *Service) loadRunRecord(ctx context.Context, runID string) (*artlistRunRecord, error) {
	if s.mainDB == nil {
		return nil, fmt.Errorf("main database not configured")
	}
	if err := s.ensureRunSchema(ctx); err != nil {
		return nil, err
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
	if err := s.ensureRunSchema(ctx); err != nil {
		return nil, err
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

func scanRunRecord(row *sql.Row) (*artlistRunRecord, error) {
	var rec artlistRunRecord
	var dryRunInt int
	var lastProcessedAt sql.NullString
	var startedAt sql.NullString
	var endedAt sql.NullString
	if err := row.Scan(
		&rec.RunID,
		&rec.Term,
		&rec.RootFolderID,
		&rec.Strategy,
		&dryRunInt,
		&rec.Status,
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
	if lastProcessedAt.Valid {
		rec.LastProcessedAt = &lastProcessedAt.String
	}
	if startedAt.Valid {
		rec.StartedAt = &startedAt.String
	}
	if endedAt.Valid {
		rec.EndedAt = &endedAt.String
	}
	return &rec, nil
}

func (s *Service) runRecordToResponse(rec *artlistRunRecord) *RunTagResponse {
	if rec == nil {
		return &RunTagResponse{OK: false, Status: "not_found", Error: "run not found"}
	}
	return &RunTagResponse{
		OK:             rec.Status != "failed",
		RunID:          rec.RunID,
		Status:         rec.Status,
		Term:           rec.Term,
		Strategy:       rec.Strategy,
		DryRun:         rec.DryRun,
		RootFolderID:   rec.RootFolderID,
		TagFolderID:    rec.TagFolderID,
		Found:          rec.Found,
		Processed:      rec.Processed,
		Skipped:        rec.Skipped,
		Failed:         rec.Failed,
		EstimatedSize:  rec.EstimatedSize,
		LastProcessedAt: rec.LastProcessedAt,
		StartedAt:      rec.StartedAt,
		EndedAt:        rec.EndedAt,
	}
}

func (s *Service) ensureRunSchema(ctx context.Context) error {
	if s.mainDB == nil {
		return fmt.Errorf("main database not configured")
	}

	stmts := []string{
		`CREATE TABLE IF NOT EXISTS artlist_runs (
			run_id TEXT PRIMARY KEY,
			term TEXT NOT NULL,
			root_folder_id TEXT NOT NULL,
			strategy TEXT NOT NULL DEFAULT 'skip',
			dry_run INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'queued',
			active_key TEXT NOT NULL DEFAULT '',
			found INTEGER NOT NULL DEFAULT 0,
			processed INTEGER NOT NULL DEFAULT 0,
			skipped INTEGER NOT NULL DEFAULT 0,
			failed INTEGER NOT NULL DEFAULT 0,
			estimated_size INTEGER NOT NULL DEFAULT 0,
			last_processed_at TEXT,
			request_json TEXT NOT NULL DEFAULT '{}',
			error TEXT NOT NULL DEFAULT '',
			tag_folder_id TEXT DEFAULT '',
			started_at TEXT NOT NULL DEFAULT (datetime('now')),
			ended_at TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_artlist_runs_term_status ON artlist_runs (term, status)`,
		`CREATE INDEX IF NOT EXISTS idx_artlist_runs_started_at ON artlist_runs (started_at DESC)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_artlist_runs_active_key ON artlist_runs (active_key) WHERE active_key != ''`,
	}

	for _, stmt := range stmts {
		if _, err := s.mainDB.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}
