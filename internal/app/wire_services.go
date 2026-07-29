// Package app — wire_services.go (PUBLIC ENTRY POINTS, July 2026 split).
//
// Split rationale (stage-based; the user's literal stock|voiceover|
// artlist|images split doesn't fit this cross-cutting root — see
// wire_services_startup_plan.go header for the explicit deviation):
//
//   - wire_services.go               : THIS FILE. Public entry points —
//     InitComposition (full server),
//     WireMinimal (minimal-mode server),
//     minimalLifecycle (stub for
//     WireMinimal callers).
//
//   - wire_services_composition.go   : COMPOSITION flow. Opens DBs +
//     migrations, calls NewComposition
//     (builds the *wiring.ComposeRoot with 12
//     bundles), constructs the local
//     broker + JobFinalizer, starts
//     background jobs, builds the
//     cleanup func.
//
//   - wire_services_orchestration.go : RUNTIME orchestration. WireServices
//     body: log sink setup + Registry
//     wiring + CleanupStack + broker
//     re-cast + assetSvc/workerHandler
//     construction + readiness probes +
//     ServerLifecycle + AppDeps packaging.
//
//   - wire_services_startup_plan.go  : Pure StartupStep builder.
//     drive-init → qdrant-collection →
//     outbox-pool → chrome-pool-prewarm
//     (Required when ImagesEnabled) +
//     jobs.startupPlan append. All
//     prerequisite gates consolidated
//     here (validateQdrantIndexer
//     Compatibility + the
//     CollectionManager-nil check).
//
// The split reduces what was a single 560-LOC file (mixed entry +
// composition + orchestration + inline startup-plan) to 4 files
// averaging ~150 LOC each, each with one stage-aligned responsibility
// + cross-file deps documented in its header.
//
// godlike/06 SSOT: the *wiring.ComposeRoot contract produced here is
// canonical; no caller constructs a *wiring.ComposeRoot-shaped value outside
// NewComposition + the test fixtures in composition_ordering_test.go.
// The split does NOT change wiring.ComposeRoot fields, AppDeps fields, or
// any public function signature.
package app

import (
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"context"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// InitComposition returns the unified *wiring.ComposeRoot tree directly.
// PR4d-final (June 2026): the legacy *CoreDeps projection was deleted —
// the public entry point now returns *wiring.ComposeRoot + *backgroundJobs so
// callers can start the JobRunner AFTER WireRegistry has registered all
// handlers.
func InitComposition(cfg *config.Config, log *zap.Logger) (*wiring.ComposeRoot, *backgroundJobs, wiring.CleanupFunc, error) {
	return initCompositionMinimal(cfg, log, "")
}

// WireMinimal creates a minimal server with core services only.
// Uses InitComposition to build the full *wiring.ComposeRoot (so background jobs,
// migrations, and DB are wired identically to WireServices), but returns
// an empty registry so the caller can mount routes selectively.
func WireMinimal(cfg *config.Config, log *zap.Logger, mode string) (*AppDeps, error) {
	_, _, coreClean, err := initCompositionMinimal(cfg, log, mode)
	if err != nil {
		return nil, err
	}
	return &AppDeps{
		Handlers: AppHandlers{
			Registry: nil, // forward-pointer: PR-COMPOSITION-REGISTRY-LIVE-WIRE
		},
		Runtime: AppRuntime{
			Lifecycle: &minimalLifecycle{stop: coreClean},
		},
		Images: AppImage{},
	}, nil
}

// minimalLifecycle wraps a single stop function as a LifecycleManager.
// Used by WireMinimal to keep the AppDeps contract uniform: all callers
// use Lifecycle.Stop for teardown, never a separate Cleanup func.
//
// QDRANT-005 (June 2026): minimalLifecycle now also implements AddProbe
// (no-op) so it stays compatible with the extended api.LifecycleManager
// interface. WireMinimal callers don't get the readiness barrier —
// the AddProbe call simply pushes the probe onto the discard tray.
type minimalLifecycle struct {
	stop func()
}

func (m *minimalLifecycle) Start(_ context.Context) error { return nil }
func (m *minimalLifecycle) Stop(_ context.Context) error {
	if m.stop != nil {
		m.stop()
	}
	return nil
}

// AddProbe is a no-op for minimalLifecycle. WireMinimal does not run
// the readiness barrier; the probe is discarded so the call site remains
// type-safe.
func (m *minimalLifecycle) AddProbe(_ string, _ func(context.Context) error) {}
