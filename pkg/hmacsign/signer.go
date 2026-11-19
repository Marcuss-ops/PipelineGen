// Package hmacsign is the canonical HMAC-SHA256 signing helper used by
// the delivery.requested outbox handler. It exposes the PRODUCER half
// only:
//
//   - Sign: produce a signature for outbound HTTP POSTs.
//
// Verification lives with the receiver. Nothing in this repository is an
// inbound webhook receiver, so the previous Verify* helpers had no
// caller and were retired on 2026-11-19 (see
// docs/operations/TICKET-PKG-DEAD-HELPER-SWEEP-2026-11-19.md). A future
// in-process receiver must implement verification against the canonical
// string documented below, reject timestamps outside its replay window
// and compare digests in constant time; it must ship with a test that
// rejects a tampered body and a rotated-out secret.
//
// Canonical signing string (negotiated with downstream receivers):
//
//	<event_timestamp>.<event_id>.<raw_body>
//
// Where:
//
//	<event_timestamp> is RFC3339 UTC, e.g. "2026-06-20T15:42:00Z".
//	<event_id>       is the producer's UUID for this event.
//	<raw_body>       is the byte-for-byte body bytes, NOT re-serialised.
//
// The signature is the lowercase hex of HMAC-SHA256(secret, that string).
// Wire format:
//
//	X-Velox-Signature:  sha256=<hex>
//	X-Velox-Timestamp:  <event_timestamp>
//	X-Velox-Event-ID:   <event_id>
//
// Secret rotation: the producer signs with the current secret only.
// Receivers that support rotation accept the current secret plus the
// previous one for one rotation window.
package hmacsign

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Header names — exported so other producers/receivers reference the
// same constants (no free-form strings, no typos).
const (
	HeaderSignature = "X-Velox-Signature"
	HeaderTimestamp = "X-Velox-Timestamp"
	HeaderEventID   = "X-Velox-Event-ID"

	// SignaturePrefix is the standard wire prefix; receivers MUST parse on
	// the substring "sha256=" (case-insensitive is too loose — keep
	// lowercase).
	SignaturePrefix = "sha256="
)

// CanonicalString returns the bytes that the HMAC is computed over.
// Format: <timestamp>.<event_id>.<body> with literal dots.
//
// Documented in the package preamble. Constructing it via fmt.Sprintf
// allocates; for large bodies the helper is fine because we only call
// it from delivery.go's outbound path (low rate, single goroutine).
func CanonicalString(timestamp string, eventID string, body []byte) []byte {
	var sb strings.Builder
	sb.Grow(len(timestamp) + len(eventID) + len(body) + 2)
	sb.WriteString(timestamp)
	sb.WriteByte('.')
	sb.WriteString(eventID)
	sb.WriteByte('.')
	sb.Write(body)
	return []byte(sb.String())
}

// Sign returns the wire-format signature for the canonical string. The
// produced value is suitable for the X-Velox-Signature header:
//
//	X-Velox-Signature:  sha256=<hex>
//
// secret MUST be ≥32 bytes (the config layer's Validate enforces this
// in production; the helper itself accepts arbitrary lengths so unit
// tests can exercise short-secret branches).
func Sign(secret []byte, timestamp string, eventID string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(CanonicalString(timestamp, eventID, body))
	return SignaturePrefix + hex.EncodeToString(mac.Sum(nil))
}
