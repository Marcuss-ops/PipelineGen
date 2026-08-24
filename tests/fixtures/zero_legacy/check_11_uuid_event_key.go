//go:build ignore

// Package fixture — self-check fixture for Check 11 (TODO 16 follow-up,
// June 2026).
//
// This file (check_11_uuid_event_key.go) demonstrates the
// event_key construction with random UUID anti-pattern. The canonical
// event_key shape is deterministic (delete:<asset_id> or
// reindex:<asset_id>:<hash>); uuid.NewString in the key shape forces
// every enqueue to produce a new outbox row, defeating
// ON CONFLICT(event_key) DO NOTHING.
//
// The fixture provides THREE functions so the self-check validates all
// three Check 11 patterns:
//
//  1. badUuidEventKeyInline      — single-line shape (caught by the
//     `eventKey.*uuid\.NewString` pattern).
//  2. badUuidEventKeyForward     — multi-line shape with `eventKey` on
//     line N and `eventID := uuid.NewString()`
//     on line N+1 (caught by the
//     `eventKey[^\n]*\n...\nuuid\.NewString`
//     pattern).
//  3. badUuidEventKeyReverse     — multi-line shape with the
//     `uuid.NewString()` call on a line
//     ABOVE the `eventKey` assignment,
//     matching the exact production code
//     shape at
//     cmd/admin/reconcile_qdrant.go:413-414
//     (caught by the reverse
//     `uuid\.NewString[^\n]*\n...\neventKey`
//     pattern).
//
// The multi-line shape is the dangerous one because stashing the random
// UUID in an intermediate variable before appending it to eventKey is a
// deliberate obfuscation — the variable name (`eventID`) hides the uuid
// origin from a single-line pattern. The lint MUST be multiline to
// catch this real-world shape.
package fixture

import "github.com/google/uuid"

// Forbidden: `eventKey` constructed with a random UUID suffix on the
// same line. The canonical pattern is `eventKey := "delete:" + assetID`
// (see internal/platform/sqlite/outbox/delete_envelope.go)
// or the index envelope in outboxevents/repository.go.
func badUuidEventKeyInline(assetID string) string {
	eventKey := "reconcile:delete:" + assetID + ":" + uuid.NewString() // anti-pattern: random UUID in key
	return eventKey
}

// Forbidden: `eventKey` constructed from an intermediate `eventID` var
// that was assigned via uuid.NewString() on a SEPARATE line BELOW.
// A single-line pattern (`eventKey.*uuid\.NewString`) misses this shape
// because the uuid is hidden behind the intermediate var. The forward
// multi-line pattern catches it.
func badUuidEventKeyForward(assetID string) string {
	eventKey := "reconcile:delete:" + assetID + ":" // anti-pattern: random UUID in key (intermediate var)
	eventID := uuid.NewString()
	return eventKey + eventID
}

// Forbidden: same multi-line shape but with the uuid.NewString() call
// appearing BEFORE the eventKey assignment — the exact production code
// shape at cmd/admin/reconcile_qdrant.go:413-414. This catches the
// reverse-order multi-line pattern (`uuid\.NewString[^\n]*\n...\neventKey`).
func badUuidEventKeyReverse(assetID string) string {
	eventID := uuid.NewString() // anti-pattern: random UUID in key (intermediate var)
	eventKey := "reconcile:delete:" + assetID + ":" + eventID
	return eventKey
}
