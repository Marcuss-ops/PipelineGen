package m2m

// store_admin.go implements the admin key-minting half of the store:
// POST/GET/DELETE /api/v1/admin/m2m/keys. It is kept beside the runtime
// lookup so both halves hash secrets with the same primitive and read/write
// the same columns.

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/middleware"
)

// Sentinel aliases keep existing store tests/callers working while the HTTP
// layer maps the port-level errors.
var (
	ErrClientExists   = middleware.ErrM2MClientExists
	ErrClientNotFound = middleware.ErrM2MClientNotFound
)

// CreateClient mints a client, persists only SHA-256(secret), and returns the
// plaintext secret exactly once. The caller must never log it.
func (s *Store) CreateClient(ctx context.Context, params middleware.M2MKeyCreateParams) (*middleware.M2MClient, string, error) {
	if s == nil || s.db == nil {
		return nil, "", sql.ErrConnDone
	}
	clientID := strings.TrimSpace(params.ClientID)
	if clientID == "" {
		return nil, "", errors.New("client_id is required")
	}
	if len(params.Scopes) == 0 {
		return nil, "", errors.New("scopes is required")
	}

	secret, err := generateSecret()
	if err != nil {
		return nil, "", err
	}
	scopesJSON, err := json.Marshal(params.Scopes)
	if err != nil {
		return nil, "", err
	}

	rateLimitRPS := params.RateLimitRPS
	if rateLimitRPS <= 0 {
		rateLimitRPS = 2
	}
	rateLimitBurst := params.RateLimitBurst
	if rateLimitBurst <= 0 {
		rateLimitBurst = 10
	}
	quotaScenes := params.QuotaMaxScenes
	if quotaScenes <= 0 {
		quotaScenes = 1000
	}
	quotaSecs := params.QuotaMaxTotalSecs
	if quotaSecs <= 0 {
		quotaSecs = 14400
	}

	_, err = s.db.ExecContext(ctx, `INSERT INTO m2m_clients
    (client_id, description, secret_hash, scopes_json, enabled,
     rate_limit_rps, rate_limit_burst, quota_max_scenes, quota_max_total_secs)
VALUES (?, ?, ?, ?, 1, ?, ?, ?, ?)`,
		clientID, strings.TrimSpace(params.Description), s.HashClientSecret(secret), string(scopesJSON),
		rateLimitRPS, rateLimitBurst, quotaScenes, quotaSecs,
	)
	if err != nil {
		// SQLite reports a UNIQUE violation as a text error. Keep the mapping
		// narrow: a duplicate client_id is a 409, any other failure is a 500.
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, "", ErrClientExists
		}
		return nil, "", err
	}

	return &middleware.M2MClient{
		ClientID:          clientID,
		Scopes:            params.Scopes,
		Enabled:           true,
		RateLimitRPS:      rateLimitRPS,
		RateLimitBurst:    rateLimitBurst,
		QuotaMaxScenes:    quotaScenes,
		QuotaMaxTotalSecs: quotaSecs,
	}, secret, nil
}

// ListClients returns every provisioned client without secret material.
func (s *Store) ListClients(ctx context.Context) ([]middleware.M2MKeyRecord, error) {
	if s == nil || s.db == nil {
		return nil, sql.ErrConnDone
	}
	rows, err := s.db.QueryContext(ctx, `SELECT client_id, description, scopes_json, enabled,
       rate_limit_rps, rate_limit_burst, quota_max_scenes, quota_max_total_secs,
       created_at, COALESCE(last_used_at, '')
FROM m2m_clients ORDER BY client_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]middleware.M2MKeyRecord, 0)
	for rows.Next() {
		var record middleware.M2MKeyRecord
		var scopesJSON string
		var enabled int
		if err := rows.Scan(
			&record.ClientID, &record.Description, &scopesJSON, &enabled,
			&record.RateLimitRPS, &record.RateLimitBurst, &record.QuotaMaxScenes,
			&record.QuotaMaxTotalSecs, &record.CreatedAt, &record.LastUsedAt,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(scopesJSON), &record.Scopes); err != nil {
			return nil, err
		}
		record.Enabled = enabled != 0
		out = append(out, record)
	}
	return out, rows.Err()
}

// RevokeClient disables a client (enabled=0) without deleting the row, so the
// audit trail and its historical jobs stay attributable.
func (s *Store) RevokeClient(ctx context.Context, clientID string) error {
	if s == nil || s.db == nil {
		return sql.ErrConnDone
	}
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return errors.New("client_id is required")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE m2m_clients SET enabled = 0 WHERE client_id = ?`, clientID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrClientNotFound
	}
	return nil
}

func generateSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "velox_m2m_" + hex.EncodeToString(buf), nil
}

var _ middleware.M2MKeyAdminPort = (*Store)(nil)
