package adminconsole

import "context"

// AuditEntry records one administrative mutation for later inspection.
type AuditEntry struct {
	ID             string         `json:"id"`
	EntityType     string         `json:"entity_type"`
	EntityID       string         `json:"entity_id"`
	Action         string         `json:"action"`
	PreviousJSON   string         `json:"previous_json,omitempty"`
	NextJSON       string         `json:"next_json,omitempty"`
	ChangedFields  map[string]any `json:"changed_fields,omitempty"`
	Actor          string         `json:"actor"`
	RequestID      string         `json:"request_id,omitempty"`
	IdempotencyKey string         `json:"idempotency_key,omitempty"`
	CreatedAt      string         `json:"created_at"`
}

// AuditLogger is the write-side port for the admin console audit log.
// Implementations must be safe for concurrent use.
type AuditLogger interface {
	Log(ctx context.Context, entry AuditEntry) error
}

// NoOpAuditLogger discards every entry. It is the safe default when no
// persistent audit store is wired yet.
type NoOpAuditLogger struct{}

// Log implements AuditLogger as a no-op.
func (NoOpAuditLogger) Log(context.Context, AuditEntry) error { return nil }

// Compile-time check.
var _ AuditLogger = (*NoOpAuditLogger)(nil)
