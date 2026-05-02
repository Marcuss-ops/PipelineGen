package jobs

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"velox/go-master/pkg/timeutil"
)

type EventType string

const (
	EventTypeCreated    EventType = "job_created"
	EventTypeQueued    EventType = "job_queued"
	EventTypeRunning   EventType = "job_running"
	EventTypeSucceeded EventType = "job_succeeded"
	EventTypeFailed    EventType = "job_failed"
	EventTypeCancelled EventType = "job_cancelled"
	EventTypeRetrying  EventType = "job_retrying"
	EventTypeDownloadStarted EventType = "download_started"
	EventTypeDownloadFinished EventType = "download_finished"
	EventTypeFFmpegStarted   EventType = "ffmpeg_started"
	EventTypeFFmpegFinished  EventType = "ffmpeg_finished"
	EventTypeUploadStarted   EventType = "upload_started"
	EventTypeUploadFinished  EventType = "upload_finished"
)

type JobEvent struct {
	ID        string
	JobID     string
	Type      EventType
	Message   string
	DataJSON  string
	CreatedAt time.Time
}

type EventsStore interface {
	CreateEvent(ctx context.Context, event *JobEvent) error
	ListEvents(ctx context.Context, jobID string) ([]JobEvent, error)
}

type SQLiteEventsStore struct {
	db *sql.DB
}

func NewSQLiteEventsStore(db *sql.DB) *SQLiteEventsStore {
	return &SQLiteEventsStore{db: db}
}

func (s *SQLiteEventsStore) CreateEvent(ctx context.Context, event *JobEvent) error {
	query := `
		INSERT INTO job_events (id, job_id, type, message, data_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`
	now := time.Now()
	event.CreatedAt = now

	_, err := s.db.ExecContext(ctx, query,
		event.ID, event.JobID, string(event.Type),
		event.Message, event.DataJSON,
		timeutil.FormatRFC3339(now),
	)
	if err != nil {
		return fmt.Errorf("failed to create event: %w", err)
	}
	return nil
}

func (s *SQLiteEventsStore) ListEvents(ctx context.Context, jobID string) ([]JobEvent, error) {
	query := `
		SELECT id, job_id, type, message, data_json, created_at
		FROM job_events
		WHERE job_id = ?
		ORDER BY created_at ASC
	`
	rows, err := s.db.QueryContext(ctx, query, jobID)
	if err != nil {
		return nil, fmt.Errorf("failed to list events: %w", err)
	}
	defer rows.Close()

	var events []JobEvent
	for rows.Next() {
		var event JobEvent
		var eventType string
		var createdAt string

		err := rows.Scan(
			&event.ID, &event.JobID, &eventType,
			&event.Message, &event.DataJSON, &createdAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan event: %w", err)
		}

		event.Type = EventType(eventType)
		event.CreatedAt = timeutil.ParseRFC3339String(&createdAt)
		events = append(events, event)
	}

	return events, nil
}
