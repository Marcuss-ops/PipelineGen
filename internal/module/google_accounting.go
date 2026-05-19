package module

import (
	"context"
	"velox/go-master/internal/api/handlers/google_accounting"
	"velox/go-master/internal/config"

	"go.uber.org/zap"
)

// NewGoogleAccountingModule creates a new GoogleAccounting module
func NewGoogleAccountingModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *google_accounting.Handler,
) *RouteModule {
	return NewRouteModule(
		"google-accounting",
		func(cfg *config.Config) bool { return cfg.Features.GoogleAccountingEnabled },
		"/google-accounting",
		handler,
		log,
		WithStart(func(ctx context.Context) error {
			log.Info("starting google-accounting module")
			return nil
		}),
		WithStop(func(ctx context.Context) error {
			log.Info("stopping google-accounting module")
			return nil
		}),
	)
}
