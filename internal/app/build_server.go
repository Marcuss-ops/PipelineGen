// Package app — BuildServer wires the composition root + HTTP server
// and returns a ServerRuntime consumed by (*ServerRuntime).Run(ctx).
//
// Caller-side prerequisites (NOT enforced here):
//   - cfg has been loaded via config.GetFromPath + cfg.Validate
//   - log has been initialized via logging.Init
//   - mode has been validated against the allowed set
//
// BuildServer collapses 9 NewServerWithHealth dependencies into one
// composition-root call so cmd/server/main.go stays a slim transport
// shell. Run drives the HTTP server + lifecycle until ctx is done;
// OS signal ownership lives in the caller (mirrors workerruntime.Run).
package app

import (
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
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
		// FASE 7 routing singleton from app.wiring.DomainBundle; held on the
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
