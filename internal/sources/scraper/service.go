package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"

	"velox/go-master/internal/config"
	"go.uber.org/zap"
)

type ScraperService struct {
	cfg *config.Config
	log *zap.Logger
}

func NewScraperService(cfg *config.Config, log *zap.Logger) *ScraperService {
	return &ScraperService{
		cfg: cfg,
		log: log,
	}
}

type SearchResult struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

func (s *ScraperService) SearchArtlist(ctx context.Context, query string) ([]SearchResult, error) {
	s.log.Info("starting artlist search", zap.String("query", query))

	// Use NodeScraperDir from config
	scriptPath := filepath.Join(s.cfg.Paths.NodeScraperDir, "artlist_search.js")
	
	// Prepare command
	cmd := exec.CommandContext(ctx, "node", scriptPath, query)
	
	output, err := cmd.CombinedOutput()
	if err != nil {
		s.log.Error("node-scraper failed", zap.Error(err), zap.String("output", string(output)))
		return nil, fmt.Errorf("scraper execution failed: %w", err)
	}

	var results []SearchResult
	if err := json.Unmarshal(output, &results); err != nil {
		return nil, fmt.Errorf("failed to parse scraper output: %w", err)
	}

	return results, nil
}
