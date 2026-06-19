package app

import (
	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/api/scraper"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"go.uber.org/zap"
)

// ScraperWiring holds the Scraper module wiring.
type ScraperWiring struct {
	Handler *scraper.ScraperHandler
	Module  module.Module
}

// WireScraper creates the Scraper handler and module.
func WireScraper(cfg *config.Config, log *zap.Logger) (*ScraperWiring, error) {
	handler := scraper.NewScraperHandler(cfg.External.NodeScraperDir)
	mod := scraper.NewModule(log, handler)
	log.Info("created Scraper module")
	return &ScraperWiring{Handler: handler, Module: mod}, nil
}
