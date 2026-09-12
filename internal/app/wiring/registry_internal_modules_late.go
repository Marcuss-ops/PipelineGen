// Package wiring — registry_internal_modules_late.go owns the tail of the
// internal-module registration: the Jobs route bundle and the late-binding
// handler preparation phase.
//
// Split out of registry_internal_modules.go (September 2026) to keep that file
// under the max_lines_per_file_strict ceiling. The split is mechanical: both
// functions already declared the same package-level types (ComposeRoot,
// RegistryWiring, registryCrossStepState, PreparedCapabilities) and take the
// registry only to register/decorate, so no call site or signature changed.
package wiring

import (
	"fmt"

	capjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	mediasearchapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediasearch"
	outboxapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/outbox"
	module "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"

	"go.uber.org/zap"
)

func registerJobsRoute(registry *module.Registry, log *zap.Logger, root *ComposeRoot, wiring *RegistryWiring) error {
	bundle := capjobs.NewBundleWithHistory(
		root.Jobs.Service,
		root.Jobs.Service,
		root.Jobs.History,
		func() bool { return true },
		log,
	)
	if err := registry.RegisterCapabilityModule(bundle, module.BuildContext{}); err != nil {
		return fmt.Errorf("wire registry: jobs: %w", err)
	}
	log.Info("created Jobs module")

	// PG-M2M (Aug 2026): build the M2M job surface from the SAME bundle
	// so Enqueue/Get stay single-implementation. The M2M module is
	// NOT registered in the public /api registry (it would collide
	// with the admin /jobs prefix and inherit the admin Auth guard);
	// it is plumbed through RegistryWiring → AppDeps.Handlers and
	// mounted on its own /api/v1/jobs group by the server composition.
	// Enabled closure is true so the M2M surface mounts whenever the
	// M2MSecurityPort is wired (the port's EnableM2M() is the real
	// gate inside JobClientAuthMiddleware; this closure only decides
	// whether the routes exist at all).
	m2mModule := capjobs.NewM2MJobsModule(bundle.Handler(), func() bool { return true })
	if wiring != nil {
		wiring.M2MJobsHandler = m2mModule
	}
	log.Info("created M2M Jobs module (POST + GET /:id on /api/v1/jobs)")
	return nil
}

// applyLateBindings is retained as an orchestration name for a pure handler
// preparation phase. Provider adapters and descriptor-owned providers have
// already been registered and frozen before this function is called.
func applyLateBindings(_ *module.Registry, log *zap.Logger, root *ComposeRoot, regWiring *RegistryWiring, crossStep registryCrossStepState) (PreparedCapabilities, error) {
	prepared := PreparedCapabilities{}
	if root.Outbox != nil && root.Outbox.EventsRepo != nil {
		regWiring.OutboxHandler = outboxapi.NewHandler(newOutboxMonitorAdapter(root.Outbox.EventsRepo), log)
	}
	// Media-search transport follows the canonical media plane, not the legacy
	// Qdrant process bundle. When PostgreSQL is deployed, readiness reports any
	// missing semantic dependency through the handler instead of hiding the route.
	if root != nil && root.MediaPostgres != nil && crossStep.SearchAggregator != nil {
		searchAgg := mediasearchapi.AggregatorSearcher(crossStep.SearchAggregator)
		regWiring.MediasearchHandler = mediasearchapi.NewHandler(mediasearchapi.WireParams{
			Aggregator: searchAgg, SemanticReady: WireMediasearchReadiness(root, searchAgg), Log: log,
		})
	}
	return prepared, nil
}
