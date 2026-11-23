package httpserver

import (
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/capabilities/system/health"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/buildinfo"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver/transport"
	infraoverlays "github.com/Marcuss-ops/PipelineGen/internal/platform/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/rendercacheinfo"
)

// registerHealthRoutes wires the unified health surface: /health with
// ?deep=true aggregation, /ready and /models (E5 + SigLIP probes). The
// /pgvector/{live,ready} endpoints were removed with the legacy vector engine on
// 2026-11-17. Returns the *transport.HealthHandler so Setup() can attach
// the WireRegistry once every route is mounted.
//
// PR1 + codex/health-ready-contract (June 2026): the health service and
// ReadyChecker are wired via SetHealthService / SetReadyChecker before
// Setup() runs — /ready no longer receives nil in production.
func (r *Router) registerHealthRoutes(engine *gin.Engine, log *zap.Logger) *transport.HealthHandler {
	// Unified health check (PR1, June 2026): single /health with ?deep=true
	// for aggregated DB+Drive+JobBroker checks. The health service
	// lives in ComposeRoot.Utility.HealthService and is wired via
	// SetHealthService before Setup() runs.
	// codex/health-ready-contract (June 2026): ReadyChecker is now wired
	// via SetReadyChecker — /ready no longer receives nil in production.
	var healthHandler *transport.HealthHandler
	if r.healthSvc != nil {
		if svc, svcOk := r.healthSvc.(*systemhealth.Service); svcOk {
			var rc *systemhealth.ReadyChecker
			if r.readyChecker != nil {
				rc, _ = r.readyChecker.(*systemhealth.ReadyChecker)
			}
			healthHandler = transport.NewHealthHandler(svc, rc)
		}
	}
	if healthHandler == nil {
		log.Warn("health service not wired, health endpoints will return 503")
		healthHandler = transport.NewHealthHandler(nil, nil /* nil-by-design; integration stub only */)
	}
	// Runtime build identity (platform/buildinfo): published as the "build"
	// object on /health and /ready so answering "am I running the code I just
	// built?" never requires inspecting systemd units, `ps`, binary mtimes or
	// port owners. Wired unconditionally — identity is process-local state,
	// not an optional capability.
	healthHandler.SetBuildInfo(buildinfo.Current)
	// Overlay render cache runtime status (enabled/wired/epoch/reason). The
	// accessor is process-local state set by the composition root while wiring
	// the script render runtime, so the health surface reflects the SAME cache
	// instance the enqueuer uses instead of a re-derived guess.
	healthHandler.SetRenderCacheStatus(rendercacheinfo.Current)
	healthHandler.SetGPUAdmission(gpuAdmissionSnapshot)
	engine.GET("/health", healthHandler.Health)
	engine.GET("/ready", healthHandler.Ready)

	// /models — E5 + SigLIP model health probes (Task 10, July 2026).

	// /models — E5 + SigLIP model health probes (Task 10, July 2026).
	// nil-safe: returns 503 when the handler is not wired.
	modelsHandler := r.modelsHandler
	if modelsHandler == nil {
		log.Warn("models handler not wired, /models will return 503")
		modelsHandler = transport.NewModelsHandler("") // empty URL -> 503 responses
	}
	engine.GET("/models", modelsHandler.Models)

	return healthHandler
}

// gpuAdmissionSnapshot reads the same environment contract as the overlay
// gate. No env means the shipped 2/2; an unparsable value is published as 0
// plus parity_ok=false so /ready exposes the drift instead of hiding it.
func gpuAdmissionSnapshot() transport.GPUAdmissionStatus {
	slots := infraoverlays.DefaultGPUGateSlots
	if raw := strings.TrimSpace(os.Getenv("RENDERINGGEN_GPU_SLOTS")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			slots = n
		} else {
			slots = 0
		}
	}
	// Lanes default to the SHIPPED worker lane count, not to the admission
	// value: defaulting to slots made a deployment that raised only
	// RENDERINGGEN_GPU_SLOTS (3) report parity_ok=true while the worker still
	// executed 2 lanes — exactly the drift this probe exists to expose.
	lanes := infraoverlays.DefaultGPUGateSlots
	if raw := strings.TrimSpace(os.Getenv("RENDERINGGEN_GPU_LANES")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			lanes = n
		} else {
			lanes = 0
		}
	}
	return transport.GPUAdmissionStatus{
		Lanes:    lanes,
		Slots:    slots,
		ParityOK: lanes > 0 && slots > 0 && lanes == slots,
	}
}
