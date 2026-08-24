// Package app — Step 5c helper (registry.go split, August 2026).
//
// registerMediaMemory wires the canonical MediaMemory resolve + bindings +
// feedback surface. Extracted from registry.go to keep the orchestrator
// file slim (same package, same public surface).
package wiring

import (
	"fmt"

	module "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	mediamemoryapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	mediamemoryapp "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	"go.uber.org/zap"
)

// ── Step 5c helper ────────────────────────────────────────────────

// registerMediaMemory wires the canonical MediaMemory resolve +
// bindings + feedback surface. The resolver is backed by the Brain
// (canonical 9-level cascade); the binding service is backed by
// SQLite + outbox dispatcher.
func registerMediaMemory(registry *module.Registry, log *zap.Logger, root *ComposeRoot, searchFanOut search.SearchFanOut) error {
	if searchFanOut == nil {
		log.Warn("registerMediaMemory: searchFanOut not wired; skipping (Level 3-9 cascade unavailable)")
		return nil
	}
	if root.DB == nil {
		log.Warn("registerMediaMemory: DB not available; skipping")
		return nil
	}

	resolver, err := WireMediaMemoryResolver(searchFanOut, root.DB.DB, log)
	if err != nil {
		return fmt.Errorf("registerMediaMemory: wire resolver: %w", err)
	}

	txMgr := outbox.NewManager(root.DB.DB, log)
	eventsRepo := outboxevents.NewRepository(root.DB.DB)
	bindingSvc := WireBindingService(root.DB.DB, txMgr, eventsRepo, log)

	handler := mediamemoryapi.NewHandler(mediamemoryapi.WireParams{
		Resolver:       resolver,
		PolicyResolver: mediamemoryapp.NewResolutionPolicyResolver(),
		Bindings:       bindingSvc,
		Log:            log,
	})

	descriptor := mediamemoryapi.Build(handler, log)
	if err := tryRegisterModuleStrict(registry, log, descriptor, WithRegistrationPoint("register.MediaMemory")); err != nil {
		return fmt.Errorf("registerMediaMemory: tryRegisterModuleStrict: %w", err)
	}

	log.Info("registerMediaMemory: mediamemory module mounted")
	return nil
}
