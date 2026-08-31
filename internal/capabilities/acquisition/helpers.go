// Package acquisition — helpers.go (Stock Cutover §12-4, July 2026).
//
// Small package-local helpers used by port.go's typed-error and
// deterministic-derivation surface. Kept in a separate file from
// the port surface so future maintainers diffing "what's the
// contract?" (port.go) and "what's the helper?" (helpers.go) find
// the answer in two keystrokes.
//
// Imports are stdlib-only (no project-internal surface). This
// package is a leaf — no imports from other internal/* packages,
// matching godlike/02 §3 layering: an application-level port that
// is leaf-safe to depend on from anywhere in the consumer pyramid.

package acquisition

import (
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"strings"
	"unicode"
)

// errSentinel constructs the typed sentinel errors declared in
// port.go. We use a private constructor so future maintainers can
// re-key the error message without touching every literal — the
// sentinel identity is the *error value*, not the .Error() string.
//
// Per godlike/07: sentinel errors are plain `errors.New(...)` values
// that callers can errors.Is() against. The `fmt.Errorf(...)` wrapper
// is reserved for wrapped errors (see `wrap` below).
func errSentinel(msg string) error {
	return &acquisitionError{msg: msg}
}

// acquisitionError is a private error type. We model explicit
// typed-error semantics as a struct (not the stdlib `errorString`)
// so a future maintainer can hoist the struct into a typed-data
// envelope (e.g. `*AcquisitionError{ Rule: "INVALID_URL", Hint: "..." }`)
// without breaking the errors.Is contract.
type acquisitionError struct{ msg string }

// Error implements the stdlib error interface. The string carries
// the rule-id prefix so log scanners can grep by rule name without
// parsing human-readable text.
func (e *acquisitionError) Error() string { return e.msg }

// Wrap adds a typed-suffix to a sentinel while preserving the
// errors.Is identity. The first arg MUST be a sentinel from the
// port's typed-error contract — `Wrap` is the canonical way to
// compose "sentinel + caller-side context" without breaking the
// errors.Is chain.
//
// The caller-side message is suffixed after the sentinel's
// canonical message so log output reads cleanly:
//
//	"acquisition: Prepare failed (network or filesystem layer): <detail>"
//
// Callers MUST inspect the typed sentinel via errors.Is; the suffix
// is for human + log enrichment only.
//
// Exported as `Wrap` so the infrastructure concrete
// (`internal/platform/acquisition/`) can compose wrapped
// errors across packages. The lowercase `wrap` was insufficient
// for cross-package callers (Go's unexported-identifier rule); the
// rename + `%w` format-verb preserves the typed-error contract.
func Wrap(sentinel error, detail string) error {
	if sentinel == nil {
		return nil
	}
	if detail == "" {
		return sentinel
	}
	// %w (NOT %s) preserves the sentinel so errors.Is/As
	// traverse the chain. The pre-§12-4-2 build used
	// fmt.Errorf("%s: %s", sentinel.Error(), detail) which broke
	// errors.Is because the sentinel's message was embedded as
	// a plain string, not chained via the error-Unwrap protocol.
	return fmt.Errorf("%w: %s", sentinel, detail)
}

// sha256Hex hashes the input string with SHA-256 and returns the
// hex-encoded digest. Used by the canonical IdempotencyKey +
// CleanupToken derivations so the staging surface is naturally
// idempotent (same input → same hex across process restarts +
// across replicas).
//
// The function is internal because the derivation is bound to the
// canonical namespace prefixes declared in port.go's
// DeriveIdempotencyKey + DeriveCleanupToken (changing the prefix
// is a wire-format break for the staging registry).
func sha256Hex(s string) string {
	sum := digest.SHA256Bytes([]byte(s))
	return sum
}

// safeBaseName sanitises an arbitrary input into a filesystem-safe
// leaf component. Strips path separators, control characters, and
// any character outside [a-zA-Z0-9._-]. Collapses runs of '_'
// separators so the result is compact + safely addressable on
// disk without escaping. Empty input returns "" — callers must
// handle the empty case separately (DeriveStageID falls back to a
// literal "stage" when the sanitised input is empty).
//
// This function is NOT a generic "filename sanitiser" — it is the
// canonical one for the staging registry's ID derivation. Future
// callers deriving sibling IDs (e.g. cache-busting sidecars) MUST
// route through this helper to keep the filesystem layout stable.
func safeBaseName(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			b.WriteRune(r)
		case r == '/', r == '\\', r == ':', r == '*',
			r == '?', r == '"', r == '<', r == '>',
			r == '|', r == ' ':
			// collapse path separators + dangerous FS chars to '_'
			b.WriteByte('_')
		case unicode.IsControl(r):
			// skip control runes
			continue
		default:
			// unknown runes (unicode letters beyond ASCII) → '_'
			b.WriteByte('_')
		}
	}
	out := b.String()
	// collapse runs of '_' to a single '_' for compactness
	var compact strings.Builder
	compact.Grow(len(out))
	prevUnder := false
	for i := 0; i < len(out); i++ {
		c := out[i]
		if c == '_' {
			if !prevUnder {
				compact.WriteByte('_')
			}
			prevUnder = true
			continue
		}
		prevUnder = false
		compact.WriteByte(c)
	}
	// strip leading/trailing underscores (so "" → "" stays "")
	// Note: rename to `result` to avoid shadowing the function parameter `s`;
	// using `s :=` in the function body collides with the parameter name and
	// triggers a "no new variables on left side of :=" Go compiler error.
	result := compact.String()
	result = strings.TrimLeft(result, "_")
	result = strings.TrimRight(result, "_")
	return result
}
