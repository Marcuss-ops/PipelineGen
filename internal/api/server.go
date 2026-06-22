// Package api provides the HTTP server for the PipelineGen system.
package api

import (
	"context"
	"fmt"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	logger "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/logging"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Server represents the HTTP server.
// Background services (maintenance, watchers, etc.) are managed externally
// by the ServiceGroup — not by the Server.
type Server struct {
	cfg        *config.Config
	router     *gin.Engine
	appRouter  *Router // reference to the Router for cleanup
	httpServer *http.Server
}

// NewServer creates a new HTTP server with module registry support.
// workerHandler (optional) is wired into /internal/v1 routes and must be
// set *before* Setup() runs so the gin engine registers the routes.
func NewServer(
	cfg *config.Config,
	registry *Registry,
	workerHandler interface{ RegisterRoutes(*gin.RouterGroup) },
) *Server {
	router := NewRouter(cfg)
	router.SetRegistry(registry)
	if workerHandler != nil {
		router.SetWorkerHandler(workerHandler)
	}
	r := router.Setup()

	return &Server{
		cfg:       cfg,
		router:    r,
		appRouter: router,
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

// Start starts the HTTP server. Background services are managed by the
// ServiceGroup in main.go — this method only handles the HTTP lifecycle.
func (s *Server) Start() error {
	logger.Info("Starting HTTP server",
		zap.String("addr", s.httpServer.Addr),
	)

	// Single signal-derived parent context for the entire server lifecycle.
	// Replaces three separate context.Background() calls (moduleCtx, shutdown
	// ctx, stopCtx) that previously had no way to inherit cancellation.
	rootCtx, rootCancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer rootCancel()

	// Module lifecycle context — derived from rootCtx so it cancels on signal.
	moduleCtx, moduleCancel := context.WithCancel(rootCtx)
	defer moduleCancel()

	// Start all enabled modules (background processes, watchers, etc.)
	if s.appRouter != nil && s.appRouter.registry != nil {
		if err := s.appRouter.registry.StartAll(moduleCtx, s.cfg); err != nil {
			return fmt.Errorf("module startup failed: %w", err)
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
		return fmt.Errorf("server listen error: %w", err)
	case <-rootCtx.Done():
		logger.Info("Shutting down server...")
	}

	// Cancel module context (signals watchdog goroutines to stop)
	moduleCancel()

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

	// Stop all enabled modules — again from a fresh background context
	// with a 10s deadline so modules get their full timeout.
	if s.appRouter != nil && s.appRouter.registry != nil {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		if err := s.appRouter.registry.StopAll(stopCtx, s.cfg); err != nil {
			logger.Error("module shutdown error", zap.Error(err))
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
