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
func (s *Store) LookupClient(ctx context.Context, secretHash string) (*middleware.M2MClient, error) {
	if s == nil || s.db == nil {
		return nil, sql.ErrConnDone
	}
	var id, scopesJSON string
	var enabled int
	var rateLimitRPS float64
	var rateLimitBurst int
	err := s.db.QueryRowContext(ctx, `SELECT client_id, scopes_json, enabled, rate_limit_rps, rate_limit_burst FROM m2m_clients WHERE secret_hash = ?`, secretHash).Scan(&id, &scopesJSON, &enabled, &rateLimitRPS, &rateLimitBurst)
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
		ClientID:       id,
		Scopes:         scopes,
		Enabled:        enabled != 0,
		RateLimitRPS:   rateLimitRPS,
		RateLimitBurst: rateLimitBurst,
	}, nil
}

var _ middleware.M2MSecurityPort = (*Store)(nil)
