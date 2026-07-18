package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	logging "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/logging"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

func main() {
	cfgPath := flag.String("config", "config.yaml", "Path to configuration file")
	host := flag.String("host", "127.0.0.1", "Host to listen on")
	port := flag.Int("port", 8100, "Port to listen on")
	apiURL := flag.String("api-url", "", "Base URL of the PipelineGen API (default: http://127.0.0.1:8000)")
	flag.Parse()

	cfg, err := config.GetFromPath(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "FATAL: failed to load config:", err)
		os.Exit(2)
	}
	if cfg == nil {
		fmt.Fprintln(os.Stderr, "FATAL: nil config from config.GetFromPath")
		os.Exit(2)
	}

	logging.Init(cfg.Logging.Level, cfg.Logging.Format)
	slog := logging.Get().Named("operator-console")
	defer func() { _ = logging.Sync() }()

	target := *apiURL
	if target == "" {
		target = fmt.Sprintf("http://127.0.0.1:%d", cfg.Server.Port)
	}
	apiBase, err := url.Parse(target)
	if err != nil {
		fmt.Fprintln(os.Stderr, "FATAL: invalid API URL:", err)
		os.Exit(2)
	}

	apiClient := NewAPIClient(apiBase.String(), cfg.Security.AdminToken)
	engine := setupRouter(cfg, apiBase, apiClient, slog)
	addr := fmt.Sprintf("%s:%d", *host, *port)
	server := &http.Server{
		Addr:         addr,
		Handler:      engine,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	slog.Info("operator console starting",
		zap.String("addr", addr),
		zap.String("api", apiBase.String()),
	)

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		slog.Fatal("operator console failed", zap.Error(err))
	case <-sigCtx.Done():
		slog.Info("shutting down operator console")
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(sigCtx), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("operator console forced shutdown", zap.Error(err))
	}
	slog.Info("operator console exited cleanly")
}

func setupRouter(cfg *config.Config, apiBase *url.URL, apiClient *APIClient, log *zap.Logger) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(middleware.RequestID())
	router.Use(gin.Recovery())

	router.Static("/static", "cmd/operator-console/static")

	tmpl := loadTemplates()
	router.SetHTMLTemplate(tmpl)

	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true, "service": "operator-console"})
	})

	// API proxy for HTMX partials
	router.Any("/api/*path", func(c *gin.Context) {
		proxyRequest(cfg, apiBase, c)
	})

	// Page routes
	router.GET("/", handleDashboardPage(apiClient))
	router.GET("/assets", handleAssetsPage(apiClient))
	router.GET("/sound-effects", handleSoundEffectsPage(apiClient))
	router.GET("/jobs", handleJobsPage(apiClient))
	router.GET("/outbox", handleOutboxPage(apiClient))

	return router
}

func proxyRequest(cfg *config.Config, apiBase *url.URL, c *gin.Context) {
	path := c.Param("path")
	target := apiBase.ResolveReference(&url.URL{
		Path:     "/" + path,
		RawQuery: c.Request.URL.RawQuery,
	})

	req, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, target.String(), c.Request.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if cfg.Security.AdminToken != "" {
		req.Header.Set("X-Velox-Admin-Token", cfg.Security.AdminToken)
	}
	req.Header.Set("Accept", "application/json")
	if ct := c.GetHeader("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/json"
	}
	c.Data(resp.StatusCode, ct, body)
}
