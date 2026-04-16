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
	"velox/go-master/internal/core/job"
	"velox/go-master/internal/core/worker"
	"velox/go-master/internal/service/maintenance"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// Server represents the HTTP server
type Server struct {
	cfg               *config.Config
	router            *gin.Engine
	appRouter         *Router        // reference to the Router for cleanup
	httpServer        *http.Server
	jobService        *job.Service
	workerService     *worker.Service
	maintenanceSvc    *maintenance.Service
	bgCancel          context.CancelFunc // cancels background goroutines on shutdown
}

// NewServerWithHandlers creates a new HTTP server with pre-constructed handlers
func NewServerWithHandlers(
	cfg *config.Config,
	jobService *job.Service,
	workerService *worker.Service,
	deps *RouterDepsWithHandlers,
) *Server {
	router := NewRouter(cfg, deps.Handlers)
	r := router.Setup()

	maintenanceSvc := maintenance.New(cfg, jobService, workerService)

	return &Server{
		cfg:           cfg,
		router:        r,
		appRouter:     router,
		jobService:    jobService,
		workerService: workerService,
		maintenanceSvc: maintenanceSvc,
		httpServer: &http.Server{
			Addr:         fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
			Handler:      r,
			ReadTimeout:  time.Duration(cfg.Server.ReadTimeout) * time.Second,
			WriteTimeout: time.Duration(cfg.Server.WriteTimeout) * time.Second,
		},
	}
}

// Start starts the HTTP server
func (s *Server) Start() error {
	logger.Info("Starting HTTP server",
		zap.String("addr", s.httpServer.Addr),
	)

	// Create a context for background goroutines
	bgCtx, bgCancel := context.WithCancel(context.Background())
	s.bgCancel = bgCancel

	// Start background maintenance tasks
	s.maintenanceSvc.Start(bgCtx)

	// Start server in a goroutine
	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("Failed to start server", zap.Error(err))
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down server...")

	// Stop background goroutines
	bgCancel()

	// Stop rate limiter cleanup goroutine
	if s.appRouter != nil {
		s.appRouter.Stop()
	}

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := s.httpServer.Shutdown(ctx); err != nil {
		logger.Error("Server forced to shutdown", zap.Error(err))
		return err
	}

	logger.Info("Server exited gracefully")
	return nil
}

// GetRouter returns the gin router (for testing)
func (s *Server) GetRouter() *gin.Engine {
	return s.router
}