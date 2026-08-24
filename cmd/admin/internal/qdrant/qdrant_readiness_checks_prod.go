// cmd/admin/qdrant_readiness_checks_prod.go — production-shaped readiness checks
// extracted from qdrant_readiness_checks.go (LONG-FILES-DECOMPOSITION-2026-07-06 Band C #1).
//
// Owns: checkDispatcherBuilt, checkWorkerRealState, checkServerProductionConstructor.
package qdrant

import (
	"context"
	"fmt"
	"strings"
)

// ── Production-shaped checks ───────────────────────────────────────────

// checkDispatcherBuilt: production-shaped. PR 15 replaces the
// config-only check (`cfg.Outbox.PollIntervalMs >= 0`) with a real
// `root.Outbox.Dispatcher != nil` assertion. The config-only check
// could pass while the dispatcher was unbuilt; the production-shaped
// check fails loudly in that case.
func checkDispatcherBuilt(_ context.Context, deps readinessDeps) checkStatus {
	if deps.Root == nil {
		return checkStatus{Err: "production composition root is nil — app.InitComposition failed; cannot verify dispatcher was built"}
	}
	if deps.Root.Dispatcher == nil {
		return checkStatus{Err: "outbox dispatcher is nil — production wiring missing"}
	}
	return checkStatus{Pass: true}
}

// checkWorkerRealState: production-shaped. Confirms the worker pool
// is real and registered (the empty-marker pattern is satisfied if
// any concrete *outboxevents.Pool has been wired).
func checkWorkerRealState(_ context.Context, deps readinessDeps) checkStatus {
	if deps.Root == nil {
		return checkStatus{Err: "production composition root is nil — cannot verify worker state"}
	}
	// Check config validity before derived objects: zero workers is a
	// configuration error that should fire regardless of pool state.
	if deps.Cfg != nil && deps.Cfg.Outbox.Workers <= 0 {
		return checkStatus{Err: fmt.Sprintf("outbox.workers=%d (must be > 0)", deps.Cfg.Outbox.Workers)}
	}
	if deps.Root.EventsPool == nil {
		return checkStatus{Err: "outbox events pool is nil — production worker pool missing"}
	}
	return checkStatus{Pass: true}
}

// checkServerProductionConstructor: production-shaped. Confirms the
// canonical InitComposition output is non-nil (mirrors cmd/server
// startup invariant per AGENTS.md §7).
func checkServerProductionConstructor(_ context.Context, deps readinessDeps) checkStatus {
	if deps.Root == nil {
		return checkStatus{Err: "production composition root is nil — app.InitComposition failed before the server can boot"}
	}
	if deps.Cfg == nil {
		return checkStatus{Err: "config is nil (composition requires cfg)"}
	}
	if strings.TrimSpace(deps.Cfg.Storage.DataDir) == "" {
		return checkStatus{Err: "storage.data_dir is empty"}
	}
	return checkStatus{Pass: true}
}
