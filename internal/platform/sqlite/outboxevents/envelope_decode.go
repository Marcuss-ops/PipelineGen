// Package outboxevents — envelope_decode.go: the SQLite-side name for the v1
// payload DECODE + VALIDATE contract.
//
// The implementation is OWNED by internal/kernel/outbox (envelope.go): the
// envelope rule — valid JSON, exact schema version, non-empty event_id,
// non-empty idempotency_key, and "a violation is TERMINAL, never retryable" —
// is storage-engine-independent, so a capability must be able to consume it
// without importing this adapter. This file re-exports the names the adapter
// and its historical callers already use.
package outboxevents

import (
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/outbox"
)

// Envelope is the mandatory v1 prefix of every outbox payload.
type Envelope = outbox.Envelope

// ViolationKind identifies which envelope check failed.
type ViolationKind = outbox.ViolationKind

const (
	ViolationParse          = outbox.ViolationParse
	ViolationSchemaVersion  = outbox.ViolationSchemaVersion
	ViolationEventID        = outbox.ViolationEventID
	ViolationIdempotencyKey = outbox.ViolationIdempotencyKey
)

// Violation is one envelope failure.
type Violation = outbox.Violation

// DecodePolicy declares the envelope contract of ONE payload schema.
type DecodePolicy = outbox.DecodePolicy

// DecodeV1 enforces the envelope contract of p on evt.PayloadJSON.
func DecodeV1(p DecodePolicy, evt Event, target any) error {
	return outbox.DecodeV1(p, evt, target)
}

// DefaultViolationError is the canonical wording of an envelope violation.
func DefaultViolationError(v Violation) error {
	return outbox.DefaultViolationError(v)
}

// EventLogFields are the correlation fields every handler log line carries.
func EventLogFields(evt Event) []zap.Field {
	return outbox.EventLogFields(evt)
}
