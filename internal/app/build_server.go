// Package app — BuildServer entry point for the cmd/server runtime.
//
// P2-1 (June 2026): this file holds the canonical server-side
// `BuildServer(cfg, mode, log) (*ServerRuntime, error)` entry point
// that lets `cmd/server/main.go` do ONLY:
//
//  1. flag parsing (--config + --mode)
//  2. fail-fast mode validation against the allowed set
//  3. config load (`config.GetFromPath` — NOT `config.Get` so typos don't
//     silently fall back to defaults) + `cfg.Validate`
//  4. logging.Init + defer logging.Sync
//  5. log.Info("server starting", ...)
//  6. `runtime, err := app.BuildServer(cfg, mode, log)`
//  7. signal.NotifyContext ctx
//  8. `runtime.Run(sigCtx)`
//
// The heavy composition-root work (WireServices + NewServerWithHealth
// + qdrantProbe wiring) moves UP from main into this layer.
//
// Pattern parallels `cmd/worker` → `internal/app/workerruntime/` (P1-3,
// June 2026): the slim `cmd/<binary>/main.go` becomes a pure transport
// shell; orchestration lives in a dedicated composition-root file with
// a `Run(ctx)` entry. The matching pair for `cmd/server` is
// `internal/app/build_server.go::BuildServer` + `(*ServerRuntime).Run`.
//
// Steps performed here (mirror the pre-P2-1 cmd/server/main.go pipeline):
//
//  1. WireServices(cfg, log, mode) — composition root (DBs, repos,
//     services, in-process job runner). Pre-P2-1 step 6.
//  2. Qdrant-probe → Lifecycle.AddProbe("qdrant", QdrantProbe.Probe)
//     — readiness-barrier extension. nil-safe when Qdrant is disabled.
//     Pre-P2-1 step 7.
//  3. api.NewServerWithHealth with ALL 9 typed-port dependencies
//     (construct-time wiring — never post-Setup setters, see QDRANT-002
//     failure mode documented in api/server.go). Pre-P2-1 step 8.
//
// What this file does NOT do (left to the slim main):
//   - --mode fail-fast validation
//   - cfg == config.GetFromPath(...) + cfg.Validate fail-fast
//   - logging.Init + defer logging.Sync (caller-side resource lifecycle)
//   - signal.NotifyContext (caller-side signal ownership — mirrors
//     worker pattern where main.go owns signal handling and hands
//     the resulting sigCtx into Run)
//
// 9-dep note: NewServerWithHealth takes 9 parameters (1 over the
// canonical 8-dep constructor cap from `architecture/policy.yaml`).
// The overage is accepted by the QDRANT-route-constructor PR (June
// 2026) because the alternative (re-introducing post-Setup setters)
// re-introduces the silent-404 bug. BuildServer collapses these 9
// arguments into a single composition-root call so cmd/server/main.go
// doesn't have to thread them through itself.
package app

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// ServerRuntime is the constructed runtime for the cmd/server binary.
// Returned by BuildServer and consumed by (*ServerRuntime).Run.
//
// The runtime is a thin transport shell: it holds the
// already-constructed *api.Server (HTTP) + *AppDeps (composition
// output) so the slim main can drive both via Run(ctx). Constructors
// for cmd/server's transport shell mirror the `workerruntime.Runtime`
// shape for cmd/worker (P1-3), minus the worker-specific background
// goroutines (heartbeat loop, registration clients) — those are
// server-side concerns handled by `deps.Lifecycle`.
type ServerRuntime struct {
	// Server is the constructed HTTP server (gin engine + http.Server
	// + lifecycle manager). Drives via StartWithContext.
	Server *api.Server

	// Deps is the composition-root output (AppDeps from wire_services.go).
	// Exposed for callers that want post-start introspection (admin CLI,
	// tests). cmd/server/main.go itself only reads `runtime.Server`.
	Deps *AppDeps
}

// BuildServer wires the composition root + HTTP server and returns
// a ServerRuntime ready for Run(ctx).
//
// Caller-side prerequisites (NOT enforced here):
//   - cfg has been loaded via config.GetFromPath + cfg.Validate
//   - log has been initialized via logging.Init
//   - mode has been validated against the allowed set
//
// These are left to the slim cmd/server/main.go to keep BuildServer
// strictly composition-shaping (mirrors how cmd/worker/main.go owns
// its --mode-ish flag handling independently of workerruntime.LoadConfig).
func BuildServer(cfg *config.Config, mode string, log *zap.Logger) (*ServerRuntime, error) {
	deps, err := WireServices(cfg, log, mode)
	if err != nil {
		return nil, fmt.Errorf("build server: wire services: %w", err)
	}

	// QDRANT-005 (June 2026): plug the Qdrant probe into the
	// lifecycle readiness barrier so /ready actually waits for Qdrant
	// reachability. nil-safe: when Qdrant is disabled at composition
	// time, the probe is nil and the AddProbe call is no-op (the
	// serverLifecycle type filters nil probes automatically inside
	// its parallel barrier).
	if deps.Health.QdrantProbe != nil && deps.Runtime.Lifecycle != nil {
		deps.Runtime.Lifecycle.AddProbe("qdrant", deps.Health.QdrantProbe.Probe)
	}

	// QDRANT-route-constructor (June 2026, PR 3): outboxHandler and
	// mediasearchHandler MUST be passed INTO NewServerWithHealth
	// (not set via setter post-Setup) so the WorkerAuth-protected
	// /internal/v1 routes register before Setup() executes.
	server := api.NewServerWithHealth(api.ServerDeps{
		Config:   cfg,
		Registry: deps.Handlers.Registry,
		Handlers: api.InternalHandlers{
			Worker:      deps.Handlers.WorkerHandler,
			Media:       deps.Handlers.InternalMediaHandler,
			Outbox:      deps.Handlers.OutboxHandler,
			MediaSearch: deps.Handlers.MediasearchHandler,
		},
		Lifecycle:    deps.Runtime.Lifecycle,
		Health:       deps.Health.HealthService,
		Ready:        deps.Health.ReadyChecker,
		QdrantHealth: deps.Health.QdrantHealth,
		// Drive /models via the cfg.ClipIndexer.ServerURL sidecar URL.
		// api.NewServerWithHealth gates this on non-empty; when set, the
		// ModelsHandler probes the sidecar at request time; when empty the
		// canonical "models sidecar not configured" 200 JSON is returned.
		ModelsSidecarURL: cfg.ClipIndexer.ServerURL,
		// FASE 7 routing singleton from app.DomainBundle; held on the
		// server for downstream handlers. nil-typed-port safe.
		ImageSearchResolver: deps.Images.ImageSearchResolver,
	})
	return &ServerRuntime{Server: server, Deps: deps}, nil
}

// Run drives the HTTP server + lifecycle until ctx is done or the
// server returns a fatal error. Caller is responsible for OS signal
// handling — see workerruntime.Run for the worker-side sibling.
//
// Pattern contract (matches workerruntime.Run semantics):
//   - main.go owns signal.NotifyContext(ctx, SIGINT, SIGTERM)
//   - main.go hands sigCtx to Run(sigCtx)
//   - Run is a thin pass-through to api.Server.StartWithContext(ctx)
//     which uses ctx as the root for the HTTP serve loop, the
//     lifecycle startup, the readiness-barrier provenance, and the
//     graceful-shutdown drain
func (r *ServerRuntime) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return r.Server.StartWithContext(ctx)
}
