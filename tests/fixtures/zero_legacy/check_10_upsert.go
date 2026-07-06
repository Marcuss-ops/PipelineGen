//go:build ignore

// Package fixture — self-check fixture for Check 10 (TODO 16).
//
// This file (check_10_upsert.go) demonstrates the asset-repo
// `.Upsert(ctx, ...)` call pattern that, when it lands in production
// handler code outside the canonical allowlist, bypasses the outbox
// dispatcher and leaves the Qdrant vector stale.
package fixture

import "context"

type clipShim struct{ ID string }

type repoShim interface {
	Upsert(ctx context.Context, c *clipShim) error
}

// Forbidden: handler code calling `repo.Upsert(ctx, ...)` directly.
// The canonical write path is outbox.Dispatcher.EnqueueAndIndex; the
// Upsert primitive on *assets.ClipsRepository is dispatcher-only.
func badHandlerBypass(ctx context.Context, r repoShim, c *clipShim) error {
	return r.Upsert(ctx, c) // anti-pattern: bypasses outbox
}
