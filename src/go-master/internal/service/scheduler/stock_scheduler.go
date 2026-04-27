package scheduler

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
	"velox/go-master/pkg/config"
)

// StockScheduler handles periodic stock clip searches
type StockScheduler struct {
	cfg     *config.Config
	log     *zap.Logger
	apiURL  string
	stopCh  chan struct{}
}

// NewStockScheduler creates a new stock scheduler
func NewStockScheduler(cfg *config.Config, log *zap.Logger) *StockScheduler {
	apiURL := fmt.Sprintf("http://%s:%d", cfg.Server.Host, cfg.Server.Port)
	return &StockScheduler{
		cfg:    cfg,
		log:    log,
		apiURL: apiURL,
		stopCh: make(chan struct{}),
	}
}

// Start begins the stock scheduler
func (s *StockScheduler) Start(ctx context.Context) {
	s.log.Info("Starting stock scheduler")

	if !s.cfg.Harvester.Enabled {
		s.log.Info("Stock scheduler disabled in config")
		return
	}

	if len(s.cfg.Harvester.SearchQueries) == 0 {
		s.log.Info("No search queries configured")
		return
	}

	// Parse check interval
	interval := 1 * time.Hour // default
	if s.cfg.Harvester.CheckInterval != "" {
		d, err := time.ParseDuration(s.cfg.Harvester.CheckInterval)
		if err == nil {
			interval = d
		}
	}

	s.log.Info("Stock scheduler started",
		zap.Duration("interval", interval),
		zap.Int("queries", len(s.cfg.Harvester.SearchQueries)))

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Run initial check
	s.processAllQueries(ctx)

	for {
		select {
		case <-ticker.C:
			s.processAllQueries(ctx)
		case <-s.stopCh:
			s.log.Info("Stock scheduler stopped")
			return
		case <-ctx.Done():
			s.log.Info("Stock scheduler stopped via context")
			return
		}
	}
}

// Stop stops the stock scheduler
func (s *StockScheduler) Stop() {
	close(s.stopCh)
}

// processAllQueries processes all configured search queries
func (s *StockScheduler) processAllQueries(ctx context.Context) {
	s.log.Info("Processing stock search queries")
	for _, query := range s.cfg.Harvester.SearchQueries {
		s.log.Info("Processing query", zap.String("query", query))
		go s.processQuery(ctx, query)
	}
}

// processQuery processes a single search query
func (s *StockScheduler) processQuery(ctx context.Context, query string) {
	// Build the request to the script generation API
	// This will trigger the full pipeline: search -> download -> process -> sync DB

	payload := strings.NewReader(fmt.Sprintf(`{
		"topic": "%s",
		"duration": 80,
		"languages": ["en"],
		"template": "documentary"
	}`, query))

	url := s.apiURL + "/api/script-docs/generate"

	resp, err := http.Post(url, "application/json", payload)
	if err != nil {
		s.log.Error("Failed to process query",
			zap.String("query", query),
			zap.Error(err))
		return
	}
	defer resp.Body.Close()

	s.log.Info("Processed query",
		zap.String("query", query),
		zap.Int("status", resp.StatusCode))
}
