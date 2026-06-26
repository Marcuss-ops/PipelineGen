// Package api provides the HTTP server for the PipelineGen system.
package api

import (
	"context"
	"fmt"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	logger "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/logging"
	pkgmw "github.com/Marcuss-ops/PipelineGen/pkg/middleware"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// LifecycleManager is the minimal lifecycle contract the server needs.
// The composition root (internal/app) implements this to manage background
// services (job runner, dispatchers, channel monitors, etc.).
//
// QDRANT-005 (June 2026) closure: the interface now exposes AddProbe so
// the readiness barrier can be EXTENDED at runtime by callers that
// wire dependencies after the lifecycle is constructed (e.g. the
// Qdrant probe in cmd/server/main.go: now done via AddProbe instead of
// silently failing through a type-assertion interface{} downsizing).
// Implementations MUST accept AddProbe calls BEFORE Start runs.
type LifecycleManager interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	// AddProbe registers an additional readiness probe. name is used
	// for logging only; duplicate-name semantics are implementation
	// defined (current impl: append, no dedup).
	AddProbe(name string, probe func(ctx context.Context) error)
}

// Server represents the HTTP server.
// Background services (maintenance, watchers, etc.) are managed externally
// by a LifecycleManager — not by the Server.
type Server struct {
	cfg        *config.Config
	router     *gin.Engine
	appRouter  *Router // reference to the Router for cleanup
	httpServer *http.Server
	lifecycle  LifecycleManager
}

// NewServer creates a new HTTP server with module registry support.
// workerHandler (optional) is wired into /internal/v1 routes and must be
// set *before* Setup() runs so the gin engine registers the routes.
// lifecycle (optional) is used for Start/Stop of background services.
// healthSvc (optional) is the application-layer health.Service; when nil,
// health endpoints return 503.
func NewServer(
	cfg *config.Config,
	registry *Registry,
	workerHandler interface{ RegisterRoutes(*gin.RouterGroup) },
	internalMediaHandler MediaInternalRouter,
	lifecycle LifecycleManager,
) *Server {
	return NewServerWithHealth(cfg, registry, workerHandler, internalMediaHandler, lifecycle, nil, nil)
}

// NewServerWithHealth creates a new HTTP server with an optional
// health-check service (PR1 Health boundary, June 2026).
// codex/health-ready-contract (June 2026): added readyChecker parameter
// so /ready receives the real ReadyChecker instead of nil.
//
// PG-006 (June 2026): every typed-port field on RouterConfig must be
// constructed via an adapter because the api package cannot import
// `internal/app` (the canonical composition root for the adapters).
// server.go is the single production caller that lives OUTSIDE the
// app-root composition boundary, so it bridges via local inline
// adapters (5 lines each) that mirror the production wrappers in
// internal/app/middleware_security_adapter.go. The duplication is
// accepted by PG-006's "no compatibility layer" rule: there is no
// shared interface — the production adapter is the wire root, the
// server.go adapters are transport bridges. Drift between the two
// must be caught by a unit-test cross-comparison; a follow-up PR
// could promote the adapters to a more shareable location, but doing
// so would require lifting the layering rule.
func NewServerWithHealth(
	cfg *config.Config,
	registry *Registry,
	workerHandler interface{ RegisterRoutes(*gin.RouterGroup) },
	internalMediaHandler MediaInternalRouter,
	lifecycle LifecycleManager,
	healthSvc interface{},
	readyChecker *systemhealth.ReadyChecker,
) *Server {
	if cfg != nil {
		// PG-006.1 (June 2026): the inline serverSecurityAdapter was deleted
		// — the canonical concrete is pkg/middleware.TokenSecurityAdapter
		// (a leaf struct reachable from internal/api, cmd/admin, and
		// internal/app without crossing layering boundaries). cfg.Security
		// is snapshotted into the canonical adapter literal here; the
		// adapter is immutable per-token-string once constructed. Enable
		// is the cfg.Security.EnableAuth passthrough (preserves the
		// pre-PG-006.1 serverSecurityAdapter.EnableAuth() semantics).
		authAdapter := &pkgmw.TokenSecurityAdapter{
			Enable: cfg.Security.EnableAuth,
			Admin:  cfg.Security.AdminToken,
			Worker: cfg.Security.WorkerToken,
		}
		rateAdapter := &serverRateLimitAdapter{cfg: cfg}
		featuresAdapter := &serverFeatureFlagsAdapter{cfg: cfg}
		router := NewRouter(&RouterConfig{
			Auth:          authAdapter,
			Rate:          rateAdapter,
			Features:      featuresAdapter,
			Log:           zap.L().Named("router"),
			ServerGinMode: cfg.Server.GinMode,
			DataDir:       cfg.Storage.DataDir,
			DownloadDir:   cfg.GoogleAccounting.DownloadDir,
			CORSOrigins:   cfg.Security.CORSOrigins,
		})
		router.SetRegistry(registry)
		if workerHandler != nil {
			router.SetWorkerHandler(workerHandler)
		}
		if internalMediaHandler != nil {
			router.SetInternalMediaHandler(internalMediaHandler)
		}
		if healthSvc != nil {
			router.SetHealthService(healthSvc)
		}
		if readyChecker != nil {
			router.SetReadyChecker(readyChecker)
		}
		r := router.Setup()

		return &Server{
			cfg:       cfg,
			router:    r,
			appRouter: router,
			lifecycle: lifecycle,
			httpServer: &http.Server{
				Addr:              fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
				Handler:           r,
				ReadTimeout:       time.Duration(cfg.Server.ReadTimeout) * time.Second,
				ReadHeaderTimeout: 15 * time.Second,
				WriteTimeout:      time.Duration(cfg.Server.WriteTimeout) * time.Second,
				IdleTimeout:       120 * time.Second,
			},
		}
	}

	router := NewRouter(&RouterConfig{Log: zap.L().Named("router")})
	router.SetRegistry(registry)
	if workerHandler != nil {
		router.SetWorkerHandler(workerHandler)
	}
	if internalMediaHandler != nil {
		router.SetInternalMediaHandler(internalMediaHandler)
	}
	if healthSvc != nil {
		router.SetHealthService(healthSvc)
	}
	if readyChecker != nil {
		router.SetReadyChecker(readyChecker)
	}
	r := router.Setup()

	return &Server{
		cfg:       cfg,
		router:    r,
		appRouter: router,
		lifecycle: lifecycle,
		httpServer: &http.Server{
			Addr:         ":0",
			Handler:      r,
			ReadTimeout:  300 * time.Second,
			WriteTimeout: 300 * time.Second,
			IdleTimeout:  120 * time.Second,
		},
	}
}

// SetLifecycle wires a LifecycleManager after construction (for callers
// that don't pass it through NewServer).
func (s *Server) SetLifecycle(lc LifecycleManager) {
	s.lifecycle = lc
}

// Start starts the HTTP server. Background services are managed by the
// LifecycleManager — this method only handles the HTTP lifecycle.
func (s *Server) Start() error {
	logger.Info("Starting HTTP server",
		zap.String("addr", s.httpServer.Addr),
	)

	// Single signal-derived parent context for the entire server lifecycle.
	rootCtx, rootCancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer rootCancel()

	// Start lifecycle-managed background services via the composition root.
	lcCtx, lcCancel := context.WithCancel(rootCtx)
	if s.lifecycle != nil {
		if err := s.lifecycle.Start(lcCtx); err != nil {
			lcCancel()
			return fmt.Errorf("lifecycle startup failed: %w", err)
		}
	}

	// Start server in a goroutine
	srvErr := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("panic in server listen goroutine", zap.Any("recover", r))
			}
			close(srvErr)
		}()
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			srvErr <- err
		}
	}()

	select {
	case err := <-srvErr:
		// Server failed to start
		lcCancel()
		return fmt.Errorf("server listen error: %w", err)
	case <-rootCtx.Done():
		logger.Info("Shutting down server...")
	}

	// Cancel lifecycle context (signals background goroutines to stop)
	lcCancel()

	// Stop rate limiter cleanup goroutine
	if s.appRouter != nil {
		s.appRouter.Stop()
	}

	// Graceful shutdown with timeout — created from a fresh background
	// context so it is NOT cancelled when rootCtx is. The 30s deadline
	// gives in-flight requests time to finish regardless of the signal
	// that triggered the shutdown.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("Server forced to shutdown", zap.Error(err))
		return fmt.Errorf("server shutdown error: %w", err)
	}

	// Stop lifecycle-managed background services.
	if s.lifecycle != nil {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		if err := s.lifecycle.Stop(stopCtx); err != nil {
			logger.Error("lifecycle shutdown error", zap.Error(err))
		}
	}

	logger.Info("Server exited gracefully")
	return nil
}

// SetWorkerHandler wires internal worker routes into the server's router.
// Delegates to Router.SetWorkerHandler.
func (s *Server) SetWorkerHandler(h interface{ RegisterRoutes(*gin.RouterGroup) }) {
	s.appRouter.SetWorkerHandler(h)
}

// SetInternalMediaHandler wires the QDRANT-001 server-to-server media
// routes (POST /internal/v1/media/sync-drive-folder) into the server's
// router. Delegates to Router.SetInternalMediaHandler.
//
// QDRANT-001 closure: the production binding is supplied by the asset
// module's storage.Handler — see internal/app/bootstrap.go or whoever
// holds the assets module after WireRegistry runs.
func (s *Server) SetInternalMediaHandler(h MediaInternalRouter) {
	s.appRouter.SetInternalMediaHandler(h)
}

// SetOutboxHandler wires the QDRANT-002 outbox monitoring handler onto
// the WorkerAuth-protected /internal/v1/outbox/* group.
// Delegates to Router.SetOutboxHandler.
func (s *Server) SetOutboxHandler(h InternalOutboxRouter) {
	s.appRouter.SetOutboxHandler(h)
}

// SetMediasearchHandler wires the QDRANT-004 mediasearch handler onto
// the WorkerAuth-protected /internal/v1/media/search route.
// Delegates to Router.SetMediasearchHandler.
func (s *Server) SetMediasearchHandler(h InternalMediaSearchRouter) {
	s.appRouter.SetMediasearchHandler(h)
}

// GetRouter returns the gin router (for testing)
func (s *Server) GetRouter() *gin.Engine {
	return s.router
}

// ── PG-006 typed-port bridges (server-scoped) ────────────────────────────
//
// PG-006.1 (June 2026): the previous serverSecurityAdapter inline struct
// was deleted. The canonical concrete is pkg/middleware.TokenSecurityAdapter
// (a leaf struct reachable from internal/api, cmd/admin, and internal/app
// without crossing layering boundaries); the cfg-wrapping trio that
// lived in api/server.go + cmd/admin/gen_api_docs.go +
// internal/app/middleware_security_adapter.go is now collapsed into
// construction-site snapshots. Only the rate-limit and feature-flags
// inline adapters remain below (their canonical equivalents are NOT
// yet tracked under pkg/middleware; a separate consolidation would
// promote them — out of scope for PG-006.1).

// serverRateLimitAdapter mirrors internal/app/middleware_security_adapter.go's
// middlewareRateLimitAdapter for the RateLimitPort surface (same nil-check
// shape as the production adapter).
type serverRateLimitAdapter struct{ cfg *config.Config }

func (a *serverRateLimitAdapter) RateLimitEnabled() bool {
	if a.cfg == nil {
		return false
	}
	return a.cfg.Security.RateLimitEnabled
}
func (a *serverRateLimitAdapter) RateLimitRequests() int {
	if a.cfg == nil {
		return 0
	}
	return a.cfg.Security.RateLimitRequests
}

// serverFeatureFlagsAdapter mirrors internal/app/middleware_security_adapter.go's
// middlewareFeatureFlagsAdapter for the FeatureFlagsPort surface (same nil-check
// shape as the production adapter).
type serverFeatureFlagsAdapter struct{ cfg *config.Config }

func (a *serverFeatureFlagsAdapter) ArtlistEnabled() bool {
	if a.cfg == nil {
		return false
	}
	return a.cfg.Features.ArtlistEnabled
}
func (a *serverFeatureFlagsAdapter) ScriptDocsEnabled() bool {
	if a.cfg == nil {
		return false
	}
	return a.cfg.Features.ScriptDocsEnabled
}
func (a *serverFeatureFlagsAdapter) ScriptClipsEnabled() bool {
	if a.cfg == nil {
		return false
	}
	return a.cfg.Features.ScriptClipsEnabled
}
