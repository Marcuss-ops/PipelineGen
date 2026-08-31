// Package adminconsole provides SQLite-backed implementations of the
// admin console ports defined in internal/capabilities/adminconsole
package adminconsole

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/adminconsole"
)

// AuditStore is the SQLite-backed adminconsole.AuditLogger.
type AuditStore struct {
	db *sql.DB
}

// NewAuditStore creates a new audit store backed by the given SQLite
// writer connection. Passing a nil connection is a compile-time-safe
// fail-closed error at construction time.
func NewAuditStore(db *sql.DB) *AuditStore {
	if db == nil {
		panic("adminconsole.NewAuditStore: nil db")
	}
	return &AuditStore{db: db}
}

// Log persists one admin mutation audit entry.
func (s *AuditStore) Log(ctx context.Context, entry adminconsole.AuditEntry) error {
	if entry.ID == "" {
		entry.ID = uuid.New().String()
	}

	previousJSON := entry.PreviousJSON
	nextJSON := entry.NextJSON
	changedFieldsJSON := ""

	if len(entry.ChangedFields) > 0 {
		b, err := json.Marshal(entry.ChangedFields)
		if err != nil {
			return fmt.Errorf("adminconsole audit: marshal changed fields: %w", err)
		}
		changedFieldsJSON = string(b)
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO admin_mutation_audit (
			id, entity_type, entity_id, action,
			previous_json, next_json, changed_fields_json,
			actor, request_id, idempotency_key, created_at,
			success, error_message
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		entry.ID, entry.EntityType, entry.EntityID, entry.Action,
		previousJSON, nextJSON, changedFieldsJSON,
		entry.Actor, entry.RequestID, entry.IdempotencyKey, entry.CreatedAt,
		boolToInt(entry.Success), entry.ErrorMessage,
	)
	if err != nil {
		return fmt.Errorf("adminconsole audit: insert: %w", err)
	}
	return nil
}

// Compile-time check.
var _ adminconsole.AuditLogger = (*AuditStore)(nil)

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
