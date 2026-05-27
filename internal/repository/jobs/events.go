package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"velox/go-master/internal/media/models"
)

func (r *Repository) AddEvent(ctx context.Context, jobID string, eventType string, message string, data map[string]any) error {
	id := fmt.Sprintf("evt_%d_%s", time.Now().UnixNano(), randomString(6))

	dataJSON, _ := json.Marshal(data)
	if dataJSON == nil {
		dataJSON = []byte("{}")
	}

	query := `INSERT INTO job_events (id, job_id, type, message, data_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`

	_, err := r.db.ExecContext(ctx, query, id, jobID, eventType, message, string(dataJSON), time.Now().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("failed to add event: %w", err)
	}

	return nil
}

func (r *Repository) ListEvents(ctx context.Context, jobID string) ([]models.JobEvent, error) {
	query := `SELECT id, job_id, type, message, created_at FROM job_events WHERE job_id = ? ORDER BY created_at ASC`

	rows, err := r.db.QueryContext(ctx, query, jobID)
	if err != nil {
		return nil, fmt.Errorf("failed to list events: %w", err)
	}
	defer rows.Close()

	var events []models.JobEvent
	for rows.Next() {
		var evt models.JobEvent
		var createdAt string
		err := rows.Scan(&evt.ID, &evt.JobID, &evt.Type, &evt.Message, &createdAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan event: %w", err)
		}
		evt.Timestamp, _ = time.Parse(time.RFC3339, createdAt)
		events = append(events, evt)
	}

	return events, nil
}

// randomString generates a random hex string of length n.
func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%0*x", n, time.Now().UnixNano())
	}
	return hex.EncodeToString(b)[:n]
}
