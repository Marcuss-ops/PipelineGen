package app

import (
	scraperapi "github.com/Marcuss-ops/PipelineGen/internal/api/scraper"
	systemapi "github.com/Marcuss-ops/PipelineGen/internal/api/system"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	module "github.com/Marcuss-ops/PipelineGen/internal/api"

	"go.uber.org/zap"
)

// SystemWiring holds the System module wiring
type SystemWiring struct {
	Module module.Module
}

// ScraperWiring holds the Scraper module wiring
type ScraperWiring struct {
	Handler *scraperapi.ScraperHandler
	Module  module.Module
}

// WireScraper creates the Scraper handler and module
func WireScraper(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*ScraperWiring, error) {
	handler := scraperapi.NewScraperHandler(cfg.External.NodeScraperDir)
	mod := scraperapi.NewModule(log, handler)
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
	mod := systemapi.NewModule(cfg, log)
	log.Info("created System module")

	return &SystemWiring{
		Module: mod,
	}
}
