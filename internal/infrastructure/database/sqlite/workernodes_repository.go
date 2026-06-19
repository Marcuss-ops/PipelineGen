package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	job "github.com/Marcuss-ops/PipelineGen/internal/jobs"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

type WorkerNodesRepository struct {
	db *sql.DB
}

type WorkerNode struct {
	ID              string
	Name            string
	Status          string
	SessionID       string
	SessionExpiresAt time.Time
	CapabilitiesJSON string
	Version         string
	Hostname        string
	LastSeenAt      time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func NewWorkerNodesRepository(db *sql.DB) *WorkerNodesRepository {
	return &WorkerNodesRepository{db: db}
}

func (r *WorkerNodesRepository) Register(ctx context.Context, cmd job.RegisterWorkerCommand) (*job.WorkerSession, error) {
	now := time.Now().UTC()
	sessionID := uuid.NewString()
	expiresAt := now.Add(90 * time.Second)

	caps, err := json.Marshal(cmd.Capabilities)
	if err != nil {
		return nil, fmt.Errorf("marshal capabilities: %w", err)
	}
	name := cmd.Name
	if name == "" {
		name = cmd.WorkerID
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO worker_nodes (
			id, name, status, session_id, session_expires_at, capabilities_json,
			version, hostname, last_seen_at, created_at, updated_at
		) VALUES (?, ?, 'active', ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			status = 'active',
			session_id = excluded.session_id,
			session_expires_at = excluded.session_expires_at,
			capabilities_json = excluded.capabilities_json,
			version = excluded.version,
			hostname = excluded.hostname,
			last_seen_at = excluded.last_seen_at,
			updated_at = excluded.updated_at
	`, cmd.WorkerID, name, sessionID, timeutil.FormatRFC3339(expiresAt), string(caps), cmd.Version, cmd.Hostname, timeutil.FormatRFC3339(now), timeutil.FormatRFC3339(now), timeutil.FormatRFC3339(now))
	if err != nil {
		return nil, fmt.Errorf("upsert worker node: %w", err)
	}
	return &job.WorkerSession{
		WorkerID:         cmd.WorkerID,
		SessionID:        sessionID,
		SessionExpiresAt: expiresAt,
		Capabilities:     cmd.Capabilities,
		Version:          cmd.Version,
		Hostname:         cmd.Hostname,
	}, nil
}

func (r *WorkerNodesRepository) Heartbeat(ctx context.Context, workerID, sessionID string, ttl time.Duration) (*job.WorkerSession, error) {
	now := time.Now().UTC()
	expiresAt := now.Add(ttl)
	res, err := r.db.ExecContext(ctx, `
		UPDATE worker_nodes
		SET session_expires_at = ?, last_seen_at = ?, updated_at = ?
		WHERE id = ? AND session_id = ? AND status = 'active'
	`, timeutil.FormatRFC3339(expiresAt), timeutil.FormatRFC3339(now), timeutil.FormatRFC3339(now), workerID, sessionID)
	if err != nil {
		return nil, fmt.Errorf("heartbeat: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, sql.ErrNoRows
	}
	return r.Get(ctx, workerID)
}

func (r *WorkerNodesRepository) Get(ctx context.Context, workerID string) (*job.WorkerSession, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, session_id, session_expires_at, capabilities_json, version, hostname
		FROM worker_nodes WHERE id = ?`, workerID)
	var id, sessionID, sessionExpiresAt, capsJSON, version, hostname string
	if err := row.Scan(&id, &sessionID, &sessionExpiresAt, &capsJSON, &version, &hostname); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	var caps job.WorkerCapabilities
	_ = json.Unmarshal([]byte(capsJSON), &caps)
	return &job.WorkerSession{
		WorkerID:         id,
		SessionID:        sessionID,
		SessionExpiresAt: timeutil.ParseRFC3339(sessionExpiresAt),
		Capabilities:     caps,
		Version:          version,
		Hostname:         hostname,
	}, nil
}

func (r *WorkerNodesRepository) IsSessionActive(ctx context.Context, workerID, sessionID string) (bool, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT session_expires_at, status
		FROM worker_nodes
		WHERE id = ? AND session_id = ? LIMIT 1
	`, workerID, sessionID)
	var expiresAt, status string
	if err := row.Scan(&expiresAt, &status); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	if status != "active" {
		return false, nil
	}
	return !timeutil.ParseRFC3339(expiresAt).IsZero() && timeutil.ParseRFC3339(expiresAt).After(time.Now().UTC()), nil
}


