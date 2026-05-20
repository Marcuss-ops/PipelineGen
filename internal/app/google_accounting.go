package app

import (
	"velox/go-master/internal/api/handlers/google_accounting"
	"velox/go-master/internal/config"
	"velox/go-master/internal/module"

	"go.uber.org/zap"
)

// GoogleAccountingWiring holds the GoogleAccounting module and handler
type GoogleAccountingWiring struct {
	Module  *module.RouteModule
	Handler *google_accounting.Handler
}

// WireGoogleAccounting wires up the GoogleAccounting module
func WireGoogleAccounting(cfg *config.Config, log *zap.Logger) (*GoogleAccountingWiring, error) {
	handler := google_accounting.NewHandler(cfg, log)
	mod := module.NewGoogleAccountingModule(cfg, log, handler)

	return &GoogleAccountingWiring{
		Module:  mod,
		Handler: handler,
	}, nil
}
