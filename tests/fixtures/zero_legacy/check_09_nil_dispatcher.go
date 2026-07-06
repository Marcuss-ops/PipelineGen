//go:build ignore

// Package fixture — self-check fixture for Check 9 (TODO 16).
//
// This file (check_09_nil_dispatcher.go) demonstrates the
// nil-dispatcher silent-fallback anti-pattern: when the dispatcher is
// nil the handler returns nil instead of failing-fast with an error.
// Silently dropping writes hides QDRANT-002 dispatcher-misconfig
// regressions.
package fixture

import "context"

type dispatcherShim interface {
	EnqueueAndIndex(ctx context.Context, x any) error
}

// Forbidden: `if dispatcher == nil { return nil }` — silent no-op when
// the canonical outbox dispatcher is missing. Hard-error patterns
// (`return fmt.Errorf("dispatcher is required")`) are fine and NOT
// caught by Check 9.
//
// The variable is canonically named `dispatcher` (matching the production
// anti-pattern) so the regex `dispatcher\s*==\s*nil\s*\{...return\s+nil`
// in the self-check matches the actual code, not a comment.
func badSilentFallback(ctx context.Context, dispatcher dispatcherShim) error {
	if dispatcher == nil {
		return nil // anti-pattern: silent no-op drops the write
	}
	return dispatcher.EnqueueAndIndex(ctx, nil)
}
