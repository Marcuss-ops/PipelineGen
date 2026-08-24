package workerruntime

import (
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/jobs/worker"
)

// BuildProfileWorkerRegistry creates a remote-worker Registry populated only
// with handlers whose job types are present in allowedTypes. This is the
// profile-gated variant of BuildWorkerRegistry (Creator Blocco 1.3, July 2026).
//
// Three invariants are enforced:
//  1. Every allowedType MUST have a handler in the Dispatcher.
//  2. The resulting registry MUST include script.generate (Creator invariant).
//  3. The returned capability slice is derived from the registry — single
//     source of truth, no manual copies.
//
// Returns worker.ErrNoHandlers if the filtered registry is empty.
func BuildProfileWorkerRegistry(root *wiring.ComposeRoot, allowedTypes []string) (*worker.Registry, []string, error) {
	if root == nil || root.Jobs == nil || root.Jobs.Dispatcher == nil {
		return nil, nil, fmt.Errorf("compose root or jobs dispatcher is nil")
	}
	if len(allowedTypes) == 0 {
		return nil, nil, fmt.Errorf("allowedTypes is empty — profile must declare at least one allowed job type")
	}

	// Build a lookup set for O(1) membership checks.
	allowed := make(map[string]struct{}, len(allowedTypes))
	for _, t := range allowedTypes {
		allowed[t] = struct{}{}
	}

	allHandlers := root.Jobs.Dispatcher.AllHandlers()

	// Gate: every allowed type MUST have a registered handler.
	// A profile that declares a type the Dispatcher doesn't know
	// about is a misconfiguration — fail at startup.
	var missing []string
	for _, t := range allowedTypes {
		if _, ok := allHandlers[t]; !ok {
			missing = append(missing, t)
		}
	}
	if len(missing) > 0 {
		return nil, nil, fmt.Errorf("profile requires job type(s) with no registered handler: %v", missing)
	}

	// Register only handlers whose types are in the allowed set.
	// P1 #13 (July 2026): jobs.Dispatcher.AllHandlers returns
	// canonical `appjobs.Handler` values which are now Go-type-aliases
	// for `kerneljob.Handler` (the canonical SSOT in
	// internal/domain/job/handler.go). worker.Handler is the same
	// alias, so the handler passes directly — no adaptHandler bridge
	// is needed at registration time. The worker runtime translates
	// `worker.Tools` (broker facade) into `*kerneljob.JobExecutionTools`
	// at Dispatch time (registry.go::translateToolsToExecutionTools)
	// so the handler observes the canonical signature.
	reg := worker.NewRegistry()
	for jobType, h := range allHandlers {
		if _, ok := allowed[jobType]; !ok {
			continue
		}
		if err := reg.Register(jobType, h); err != nil {
			return nil, nil, fmt.Errorf("register handler for %s: %w", jobType, err)
		}
	}

	if reg.Len() == 0 {
		return nil, nil, worker.ErrNoHandlers
	}

	reg.SeedProducesArtifacts(appjobs.Compose().ProducesArtifactsMap())

	// Creator invariant: script.generate MUST be present.
	// A profile that omits script generation is a composition-level
	// misconfiguration and must fail closed.
	if !reg.Has("script.generate") {
		return nil, nil, fmt.Errorf("profile requires script.generate handler but it was not registered")
	}

	caps := reg.JobTypes()
	return reg, caps, nil
}

// BuildWorkerRegistry creates a remote-worker Registry populated with the
// same handlers wired into the in-process Dispatcher. Each handler is
// adapted so that worker.Tools is translated into appjobs.JobTools.
// The returned capability slice is derived from the registry itself — it is
// the single source of truth for what this worker can execute.
//
// Returns worker.ErrNoHandlers if the Dispatcher has zero registered
// handlers, preventing the remote worker from starting with an empty
// registry that would silently claim every job.
func BuildWorkerRegistry(root *wiring.ComposeRoot) (*worker.Registry, []string, error) {
	if root == nil || root.Jobs == nil || root.Jobs.Dispatcher == nil {
		return nil, nil, fmt.Errorf("compose root or jobs dispatcher is nil")
	}
	reg := worker.NewRegistry()
	// P1 #13 (July 2026): handler is canonical `appjobs.Handler`
	// which is a Go-type-alias for `kerneljob.Handler`. worker.Handler
	// is the same alias, so it passes directly — no adaptHandler
	// bridge is needed at registration time. The worker runtime
	// translates `worker.Tools` (broker facade) into
	// `*kerneljob.JobExecutionTools` at Dispatch time
	// (registry.go::translateToolsToExecutionTools) so the handler
	// observes the canonical signature.
	for jobType, h := range root.Jobs.Dispatcher.AllHandlers() {
		if err := reg.Register(jobType, h); err != nil {
			return nil, nil, fmt.Errorf("register handler for %s: %w", jobType, err)
		}
	}
	if reg.Len() == 0 {
		return nil, nil, worker.ErrNoHandlers
	}

	// PR-COMPLETE-WORKER-FAIL-BOOT (Aug 2026): seed the worker
	// runner's ProducesArtifacts map from the canonical job
	// registry so that artifact-producing jobs (ArtifactOwnership=
	// WorkerSpine) route through CompleteWithArtifacts instead of
	// silently falling through to the legacy Complete() path.
	// Without this, r.registry.ProducesArtifacts() always returns
	// false in production, and every artifact job hits the
	// SQL-layer ErrCompleteJobPathViolation gate after a costly
	// retry cycle.
	reg.SeedProducesArtifacts(appjobs.Compose().ProducesArtifactsMap())

	caps := reg.JobTypes()
	return reg, caps, nil
}

// adaptHandler was RETIRED in P1 #13 (July 2026): appjobs.Handler and
// worker.Handler are both Go-type-aliases for kerneljob.Handler (see
// internal/domain/job/handler.go for the canonical SSOT). Re-introducing
// a bridge here is forward-forbidden — the runtime handles worker.Tools
// → *kerneljob.JobExecutionTools translation at Dispatch time
// (worker/registry.go::translateToolsToExecutionTools). The two
// Build*WorkerRegistry helpers below now pass canonical Handler values
// directly to worker.Registry.Register.
