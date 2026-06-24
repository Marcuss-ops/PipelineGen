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
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	logger "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/logging"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// LifecycleManager is the minimal lifecycle contract the server needs.
// The composition root (internal/app) implements this to manage background
// services (job runner, dispatchers, channel monitors, etc.).
type LifecycleManager interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
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
	lifecycle LifecycleManager,
) *Server {
	return NewServerWithHealth(cfg, registry, workerHandler, lifecycle, nil, nil)
}

// NewServerWithHealth creates a new HTTP server with an optional
// health-check service (PR1 Health boundary, June 2026).
// codex/health-ready-contract (June 2026): added readyChecker parameter
// so /ready receives the real ReadyChecker instead of nil.
func NewServerWithHealth(
	cfg *config.Config,
	registry *Registry,
	workerHandler interface{ RegisterRoutes(*gin.RouterGroup) },
	lifecycle LifecycleManager,
	healthSvc interface{},
	readyChecker *systemhealth.ReadyChecker,
) *Server {
	router := NewRouter(cfg)
	router.SetRegistry(registry)
	if workerHandler != nil {
		router.SetWorkerHandler(workerHandler)
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

// GetRouter returns the gin router (for testing)
func (s *Server) GetRouter() *gin.Engine {
	return s.router
}
