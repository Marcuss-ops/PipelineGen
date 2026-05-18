package app

import (
	scraperhandler "velox/go-master/internal/api/handlers/scraper"
	"velox/go-master/internal/module"
	"velox/go-master/internal/config"

	"go.uber.org/zap"
)

// SystemWiring holds the System module wiring
type SystemWiring struct {
	Module module.Module
}

// ScraperWiring holds the Scraper module wiring
type ScraperWiring struct {
	Handler *scraperhandler.Handler
	Module  module.Module
}

// WireScraper creates the Scraper handler and module
func WireScraper(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*ScraperWiring, error) {
	handler := scraperhandler.NewHandler("node-scraper")
	mod := module.NewScraperModule(log, handler)
	log.Info("created Scraper module")

	return &ScraperWiring{
		Handler: handler,
		Module:  mod,
	}, nil
}

// WireSystem creates the System handler and module
func WireSystem(
	cfg *config.Config,
	log *zap.Logger,
) *SystemWiring {
	mod := module.NewSystemModule(cfg, log)
	log.Info("created System module")

	return &SystemWiring{
		Module: mod,
	}
}
