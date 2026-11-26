// envelope.go — the ONE implementation of the v1 outbox payload DECODE +
// VALIDATE contract.
//
// Every v1 handler used to carry its own copy of the same four steps:
// unmarshal the payload, compare schema_version, require event_id, require
// idempotency_key — each with its own warning log and its own terminal error.
// Two handlers were byte-identical for ~450 lines, differing only in the names
// substituted into the messages. That is duplication of a RULE, not of code: "a
// malformed payload is terminal, never retryable" is a policy decision, and a
// policy written sixteen times is a policy that will be updated in fifteen
// places the day it changes.
//
// This file owns the ENVELOPE half of the rule — the part that is the same for
// every event: valid JSON, exact schema version, non-empty event_id, non-empty
// idempotency_key. The RESOURCE half (asset_id, voiceover_id, ...) stays in the
// handler, because only the handler knows which field identifies its aggregate
// and what a good diagnostic for it looks like.
//
// A handler keeps its own error IDENTITY through DecodePolicy.OnViolation: the
// checks (and their order) are central, the wording and the sentinel a handler
// already exposed to tests and to the pool are not rewritten. Centralising the
// checks must never silently change what a caller can branch on.
package outbox

import (
	"encoding/json"
	"fmt"

	"go.uber.org/zap"
)

// Envelope is the mandatory v1 prefix of every outbox payload.
type Envelope struct {
	SchemaVersion  string `json:"schema_version"`
	EventID        string `json:"event_id"`
	IdempotencyKey string `json:"idempotency_key"`
	RequestedAt    string `json:"requested_at,omitempty"`
}

// ViolationKind identifies which envelope check failed.
type ViolationKind string

const (
	// ViolationParse: the payload is not a JSON document the schema accepts.
	ViolationParse ViolationKind = "payload_parse"
	// ViolationSchemaVersion: the payload declares a version this handler does
	// not interpret.
	ViolationSchemaVersion ViolationKind = "schema_version"
	// ViolationEventID: the payload carries no producer event id.
	ViolationEventID ViolationKind = "event_id"
	// ViolationIdempotencyKey: the payload carries no idempotency key.
	ViolationIdempotencyKey ViolationKind = "idempotency_key"
)

// Violation is one envelope failure. It carries the observed and expected
// values so a handler can reproduce its historical diagnostic without
// re-implementing the check.
type Violation struct {
	Kind      ViolationKind
	EventName string
	// Err is set for ViolationParse only.
	Err error
	// Got / Want are set for ViolationSchemaVersion. Want is always populated.
	Got  string
	Want string
}

// DecodePolicy declares the envelope contract of ONE payload schema.
type DecodePolicy struct {
	// EventName prefixes every diagnostic ("asset.published", ...).
	EventName string
	// WantSchemaVersion is compared exactly with the payload's schema_version.
	WantSchemaVersion string
	// SkipIdempotencyKey relaxes the idempotency check for a schema whose
	// PRODUCER does not emit the field. It is a documented exception, not a
	// convenience: naming the producer is required in the comment.
	SkipIdempotencyKey bool
	// SkipEventID relaxes the event-id check for the same reason.
	SkipEventID bool
	// OnViolation maps a violation onto the handler's own error identity. Nil
	// selects DefaultViolationError (the canonical wording).
	OnViolation func(Violation) error
	// Log receives the prescribed warning for each violation. Nil is silent.
	Log *zap.Logger
}

// DecodeV1 enforces the envelope contract of p on evt.PayloadJSON and, when
// target is non-nil, decodes the payload into it.
//
// Every violation is TERMINAL by construction: a malformed payload, a version
// the handler cannot interpret, or a missing identifier cannot be repaired by
// retrying, so the pool must dead-letter on the first attempt instead of
// burning max_attempts. A handler that adds its own error wrapper through
// OnViolation is responsible for keeping it terminal
// (outbox.NewTerminalError).
func DecodeV1(p DecodePolicy, evt Event, target any) error {
	log := p.Log
	if log == nil {
		log = zap.NewNop()
	}
	var envelope Envelope
	if err := json.Unmarshal([]byte(evt.PayloadJSON), &envelope); err != nil {
		log.Warn(p.EventName+" payload parse failed (terminal)",
			append(EventLogFields(evt), zap.Error(err))...,
		)
		return p.violation(Violation{Kind: ViolationParse, EventName: p.EventName, Err: err})
	}
	if envelope.SchemaVersion != p.WantSchemaVersion {
		log.Warn(p.EventName+" schema_version mismatch (terminal)",
			append(EventLogFields(evt),
				zap.String("got_version", envelope.SchemaVersion),
				zap.String("want_version", p.WantSchemaVersion),
			)...,
		)
		return p.violation(Violation{
			Kind: ViolationSchemaVersion, EventName: p.EventName,
			Got: envelope.SchemaVersion, Want: p.WantSchemaVersion,
		})
	}
	if envelope.EventID == "" && !p.SkipEventID {
		log.Warn(p.EventName+": missing event_id (terminal)", EventLogFields(evt)...)
		return p.violation(Violation{Kind: ViolationEventID, EventName: p.EventName})
	}
	if envelope.IdempotencyKey == "" && !p.SkipIdempotencyKey {
		log.Warn(p.EventName+": missing idempotency_key (terminal)", EventLogFields(evt)...)
		return p.violation(Violation{Kind: ViolationIdempotencyKey, EventName: p.EventName})
	}
	if target == nil {
		return nil
	}
	if err := json.Unmarshal([]byte(evt.PayloadJSON), target); err != nil {
		log.Warn(p.EventName+" payload decode failed (terminal)",
			append(EventLogFields(evt), zap.Error(err))...,
		)
		return p.violation(Violation{Kind: ViolationParse, EventName: p.EventName, Err: err})
	}
	return nil
}

func (p DecodePolicy) violation(v Violation) error {
	if p.OnViolation != nil {
		return p.OnViolation(v)
	}
	return NewTerminalError(DefaultViolationError(v))
}

// DefaultViolationError is the canonical wording of an envelope violation. It
// is exported so a handler that only needs to add a sentinel wrapper can embed
// this error instead of re-spelling the message.
func DefaultViolationError(v Violation) error {
	switch v.Kind {
	case ViolationParse:
		return fmt.Errorf("%s payload parse: %w", v.EventName, v.Err)
	case ViolationSchemaVersion:
		return fmt.Errorf("%s: schema_version mismatch (terminal — got %q, want %q)", v.EventName, v.Got, v.Want)
	case ViolationEventID:
		return fmt.Errorf("%s: event_id is required (terminal)", v.EventName)
	case ViolationIdempotencyKey:
		return fmt.Errorf("%s: idempotency_key is required (terminal)", v.EventName)
	default:
		return fmt.Errorf("%s: envelope violation %q", v.EventName, v.Kind)
	}
}

// EventLogFields are the correlation fields every handler log line carries.
// They are the same for every event, so the handler does not re-spell them.
func EventLogFields(evt Event) []zap.Field {
	return []zap.Field{
		zap.Int64("event_id", evt.ID),
		zap.String("aggregate_id", evt.AggregateID),
		zap.Int("attempt", evt.AttemptCount),
	}
}
