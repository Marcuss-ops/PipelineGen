// Package api provides the HTTP server for the VeloxEditing system.
package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"velox/go-master/internal/module"
	"velox/go-master/internal/config"
	"velox/go-master/internal/logger"
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
func NewServer(
	cfg *config.Config,
	registry *module.Registry,
) *Server {
	router := NewRouter(cfg)
	router.SetRegistry(registry)
	r := router.Setup()

	return &Server{
		cfg:       cfg,
		router:    r,
		appRouter: router,
		httpServer: &http.Server{
			Addr:         fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
			Handler:      r,
			ReadTimeout:  time.Duration(cfg.Server.ReadTimeout) * time.Second,
			WriteTimeout: time.Duration(cfg.Server.WriteTimeout) * time.Second,
		},
	}
}

// Start starts the HTTP server. Background services are managed by the
// ServiceGroup in main.go — this method only handles the HTTP lifecycle.
func (s *Server) Start() error {
	logger.Info("Starting HTTP server",
		zap.String("addr", s.httpServer.Addr),
	)

	// Start server in a goroutine
	srvErr := make(chan error, 1)
	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			srvErr <- err
		}
		close(srvErr)
	}()

	// Wait for interrupt signal or server error
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-srvErr:
		// Server failed to start
		return fmt.Errorf("server listen error: %w", err)
	case <-quit:
		logger.Info("Shutting down server...")
	}

	// Stop rate limiter cleanup goroutine
	if s.appRouter != nil {
		s.appRouter.Stop()
	}

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := s.httpServer.Shutdown(ctx); err != nil {
		logger.Error("Server forced to shutdown", zap.Error(err))
		return fmt.Errorf("server shutdown error: %w", err)
	}

	logger.Info("Server exited gracefully")
	return nil
}

// GetRouter returns the gin router (for testing)
func (s *Server) GetRouter() *gin.Engine {
	return s.router
}
