package app

import (
	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/api/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"go.uber.org/zap"
)

// ScraperWiring holds the Scraper module wiring.
type ScraperWiring struct {
	Handler *assets.ScraperHandler
	Module  module.Module
}

// WireScraper creates the Scraper handler and module.
func WireScraper(cfg *config.Config, log *zap.Logger) (*ScraperWiring, error) {
	handler := assets.NewScraperHandler(cfg.External.NodeScraperDir)
	mod := assets.NewScraperModule(log, handler)
	log.Info("created Scraper module")
	return &ScraperWiring{Handler: handler, Module: mod}, nil
}
