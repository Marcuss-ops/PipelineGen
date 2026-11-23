package m2m

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/middleware"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// Store is the SQLite adapter for scoped M2M clients. Plaintext secrets are
// never persisted; the HTTP middleware receives only this non-secret view.
type Store struct {
	db      *sql.DB
	enabled bool
}

func NewStore(db *sql.DB, enabled bool) *Store { return &Store{db: db, enabled: enabled} }
func (s *Store) EnableM2M() bool               { return s != nil && s.enabled && s.db != nil }
func (s *Store) HashClientSecret(plaintext string) string {
	return digest.SHA256String(plaintext)
}

// LookupClient resolves the client and its live quota usage. Usage is derived
// from the jobs table rather than maintained as a counter: a counter would
// drift on retries/deletes, while COUNT/SUM over the owner column is the same
// fact the job row already records.
func (s *Store) LookupClient(ctx context.Context, secretHash string) (*middleware.M2MClient, error) {
	if s == nil || s.db == nil {
		return nil, sql.ErrConnDone
	}
	var id, scopesJSON string
	var enabled int
	var rateLimitRPS float64
	var rateLimitBurst int
	var quotaMaxScenes, quotaMaxTotalSecs int64
	var usedScenes, usedTotalSecs int64
	err := s.db.QueryRowContext(ctx, `SELECT m.client_id, m.scopes_json, m.enabled, m.rate_limit_rps, m.rate_limit_burst,
       m.quota_max_scenes, m.quota_max_total_secs,
       (SELECT COUNT(*) FROM jobs j WHERE j.client_id = m.client_id) AS used_scenes,
       (SELECT COALESCE(SUM(j.duration_ms), 0) / 1000 FROM jobs j WHERE j.client_id = m.client_id) AS used_total_secs
FROM m2m_clients m WHERE m.secret_hash = ?`, secretHash).Scan(
		&id, &scopesJSON, &enabled, &rateLimitRPS, &rateLimitBurst,
		&quotaMaxScenes, &quotaMaxTotalSecs, &usedScenes, &usedTotalSecs,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var scopes []string
	if err := json.Unmarshal([]byte(scopesJSON), &scopes); err != nil {
		return nil, err
	}
	return &middleware.M2MClient{
		ClientID:           id,
		Scopes:             scopes,
		Enabled:            enabled != 0,
		RateLimitRPS:       rateLimitRPS,
		RateLimitBurst:     rateLimitBurst,
		QuotaMaxScenes:     quotaMaxScenes,
		QuotaMaxTotalSecs:  quotaMaxTotalSecs,
		QuotaUsedScenes:    usedScenes,
		QuotaUsedTotalSecs: usedTotalSecs,
	}, nil
}

// TouchClient records successful use. It is best-effort: an audit timestamp
// must never fail an authorized request.
func (s *Store) TouchClient(ctx context.Context, clientID string) error {
	if s == nil || s.db == nil || clientID == "" {
		return sql.ErrConnDone
	}
	_, err := s.db.ExecContext(ctx, `UPDATE m2m_clients SET last_used_at = datetime('now') WHERE client_id = ?`, clientID)
	return err
}

var _ middleware.M2MSecurityPort = (*Store)(nil)
