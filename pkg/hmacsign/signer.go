// Package hmacsign is the canonical HMAC-SHA256 signing helper used by
// the delivery.requested outbox handler. It exposes:
//
//   - Sign: produce a signature for outbound HTTP POSTs.
//   - Verify: validate signatures for INBOUND webhook receivers (Phase
//     6+ work — today nothing else in the codebase is an inbound
//     receiver, but any future webhook listener will use this helper).
//   - Replay-window enforcement (when Verify is called with a max age):
//     timestamps older than the window are rejected so a replayed
//     signature cannot succeed past its expiration.
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
// Rotation: Verify accepts (currentSecret, previousSecret). When the
// producer rotates they set New = old + Set Previous = old; receivers
// that haven't yet replaced their cache accept both. After the
// rotation window they drop previousSecret. The Sign helper uses only
// the current secret.
package hmacsign

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
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

// ErrInvalidSignature is returned when none of the candidate secrets
// match the supplied signature. Callers map this to HTTP 401 (or to
// outbox retry → dead_letter for sender-side validation).
var ErrInvalidSignature = errors.New("hmacsign: signature does not match any known secret")

// ErrStaleTimestamp is returned by VerifyWithReplayWindow when the
// timestamp is older than the supplied max age. This is the canonical
// replay attack defence.
var ErrStaleTimestamp = errors.New("hmacsign: timestamp is outside the replay window")

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

// Verify checks the supplied signature against ONE secret. Use
// VerifyMultipleSecrets for current+previous rotation, or
// VerifyWithReplayWindow when the caller wants replay protection too.
func Verify(secret []byte, timestamp string, eventID string, body []byte, signature string) error {
	expected := Sign(secret, timestamp, eventID, body)
	if subtleEqual(expected, signature) {
		return nil
	}
	return ErrInvalidSignature
}

// VerifyMultipleSecrets returns nil when the supplied signature matches
// ANY of the candidate secrets. Order matters for performance — pass the
// current secret FIRST so Verify short-circuits on the common path.
func VerifyMultipleSecrets(secrets [][]byte, timestamp string, eventID string, body []byte, signature string) error {
	for _, s := range secrets {
		if s == nil {
			continue
		}
		if err := Verify(s, timestamp, eventID, body, signature); err == nil {
			return nil
		}
	}
	return ErrInvalidSignature
}

// VerifyWithReplayWindow enforces both:
//   - signature matches ONE of the candidate secrets, AND
//   - timestamp is not older than maxAge before now.
//
// maxAge <= 0 disables the timestamp check (signatures never expire).
//
// Errors: ErrInvalidSignature, ErrStaleTimestamp, or a parse failure
// for the timestamp.
func VerifyWithReplayWindow(secrets [][]byte, timestamp string, eventID string, body []byte, signature string, now time.Time, maxAge time.Duration) error {
	if err := VerifyMultipleSecrets(secrets, timestamp, eventID, body, signature); err != nil {
		return err
	}
	if maxAge <= 0 {
		return nil
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(timestamp))
	if err != nil {
		return fmt.Errorf("hmacsign: parse timestamp %q: %w", timestamp, err)
	}
	if now.Sub(t) > maxAge {
		return ErrStaleTimestamp
	}
	return nil
}

// subtleEqual compares two strings in constant time. Using the standard
// == operator for cryptographic comparisons leaks timing information;
// we use crypto/subtle.ConstantTimeCompare via a byte roundtrip.
func subtleEqual(a, b string) bool {
	// Equal-length fast path that returns false without decoding hex.
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
