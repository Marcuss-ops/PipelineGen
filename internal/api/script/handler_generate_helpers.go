// Package script — handler_generate_helpers.go is the canonical home
// for the small pure helpers consumed by HandlerGenerate.Generate:
// the printable-ASCII Idempotency-Key validation function and the
// package-level enqueue timeout variable.
//
// FASE 2 (July 2026): both symbols previously lived next to the
// now-removed package-level enqueueEnvelopeFn (handler_enqueue.go,
// deleted alongside the legacy adapter). They are pure helpers with
// zero side effects, so a focused helpers file (rather than
// inlining into handler_generate_handler.go) preserves the
// godlike/06 SSOT one-canonical-owner-per-fact split.
package script

import "time"

// enqueueTimeout is the maximum time HandlerGenerate.Generate waits
// for the submission service to commit the operation + job + outbox
// event in a single transaction. It is a package-level var so tests
// can temporarily shorten it without rebuilding the binary.
//
// SCRIPTCONTRACT contract: a short timeout prevents POST /generate
// from blocking if the broker / database is congested. The 10s
// ceiling is the canonical ScriptFlow throughput window; a future
// instrumentation PR can surface actual observed commit durations
// (operations repository metrics) to right-size this constant.
var enqueueTimeout = 10 * time.Second

// isValidIdempotencyKey mirrors the printable-ASCII + max-255 rule
// applied to the Idempotency-Key HTTP header. Returns false on
// empty key, key longer than 255 chars, or any byte outside the
// printable-ASCII range (0x20..0x7E). The validation matches the
// behaviour of the canonical middleware-level Idempotency middleware
// (internal/api/middleware/idempotency.go) so a key produced
// valid at the middleware boundary cannot be rejected here.
func isValidIdempotencyKey(key string) bool {
	if len(key) == 0 || len(key) > 255 {
		return false
	}
	for _, r := range key {
		if r < 0x20 || r > 0x7E {
			return false
		}
	}
	return true
}
