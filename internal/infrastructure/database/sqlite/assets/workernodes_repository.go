package assets

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

type WorkerNodesRepository struct {
	db *sql.DB
}

type WorkerNode struct {
	ID               string
	Name             string
	Status           string
	SessionID        string
	SessionExpiresAt time.Time
	CapabilitiesJSON string
	Version          string
	Hostname         string
	LastSeenAt       time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func NewWorkerNodesRepository(db *sql.DB) *WorkerNodesRepository {
	return &WorkerNodesRepository{db: db}
}

// Register is the canonical upsert for a worker session. Per
// PR-FIX-WORKER-SESSION-REGISTER-RACE (2026-07-08): the
// ON CONFLICT clause is now IDEMPOTENT for an active session.
// If the existing row is still status='active' AND its
// session_expires_at is in the future, the OLD session_id +
// session_expires_at are preserved. Only a dead session
// (status != 'active' OR session_expires_at in the past) is
// replaced by the new uuid.NewString() session.
//
// Why: pre-fix, the ON CONFLICT unconditionally overwrote
// session_id + session_expires_at. Any periodic re-Register
// from a competing process (e.g. a doctor healthcheck running
// every 60s, a hot-spare worker, a recovery re-register) would
// silently mint a new session_id, invalidating the active
// worker's in-memory session_id. The worker's next heartbeat
// would return sql.ErrNoRows (WHERE id=? AND session_id=?
// no longer matches), and the next claim would surface
// "worker session is invalid or expired" via the broker's
// ensureSession → IsSessionActive check. The cycle repeats
// every 60s — the cadence of the periodic re-Register.
//
// After the UPSERT, we re-read via r.Get(ctx, cmd.WorkerID)
// to fetch the canonical session_id (which may be the
// preserved old one). Returning the locally-generated
// sessionID variable would lie to the caller — the DB holds
// the preserved one, not the discarded new one.
func (r *WorkerNodesRepository) Register(ctx context.Context, cmd job.RegisterWorkerCommand) (*job.WorkerSession, error) {
	now := time.Now().UTC()
	sessionID := uuid.NewString()
	expiresAt := now.Add(90 * time.Second)
	nowStr := timeutil.FormatRFC3339(now)

	caps, err := json.Marshal(cmd.Capabilities)
	if err != nil {
		return nil, fmt.Errorf("marshal capabilities: %w", err)
	}
	name := cmd.Name
	if name == "" {
		name = cmd.WorkerID
	}
	// The CASE expressions preserve the existing session_id +
	// session_expires_at if the existing row is still alive
	// (status='active' AND session_expires_at > now). SQLite
	// string-compares the RFC3339 timestamps; an existing
	// session_expires_at > now (i.e. in the future) is correctly
	// greater than nowStr lexicographically.
	//
	// nowStr is passed TWICE — once for the session_id CASE,
	// once for the session_expires_at CASE.
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO worker_nodes (
			id, name, status, session_id, session_expires_at, capabilities_json,
			version, hostname, last_seen_at, created_at, updated_at
		) VALUES (?, ?, 'active', ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			status = 'active',
			session_id = CASE
				WHEN worker_nodes.status = 'active' AND worker_nodes.session_expires_at > ?
				THEN worker_nodes.session_id
				ELSE excluded.session_id
			END,
			session_expires_at = CASE
				WHEN worker_nodes.status = 'active' AND worker_nodes.session_expires_at > ?
				THEN worker_nodes.session_expires_at
				ELSE excluded.session_expires_at
			END,
			capabilities_json = excluded.capabilities_json,
			version = excluded.version,
			hostname = excluded.hostname,
			last_seen_at = excluded.last_seen_at,
			updated_at = excluded.updated_at
	`, cmd.WorkerID, name, sessionID, timeutil.FormatRFC3339(expiresAt), string(caps), cmd.Version, cmd.Hostname, nowStr, nowStr, nowStr, nowStr, nowStr)
	if err != nil {
		return nil, fmt.Errorf("upsert worker node: %w", err)
	}
	// Fetch the canonical session state from the DB — may be the
	// preserved old session_id (idempotent path) or the new
	// uuid.NewString() (fresh / takeover path). Returning the
	// local sessionID variable would return a value the DB
	// discarded.
	return r.Get(ctx, cmd.WorkerID)
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
