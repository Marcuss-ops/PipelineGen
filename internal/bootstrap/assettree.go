package bootstrap

import (
	"velox/go-master/internal/api/handlers/assettree"
	"velox/go-master/internal/module"
	repos "velox/go-master/internal/repository/assettree"
	svcs "velox/go-master/internal/service/assettree"
	"velox/go-master/pkg/config"

	"go.uber.org/zap"
)

// AssetTreeWiring holds the Asset Tree module wiring
type AssetTreeWiring struct {
	Handler *assettree.Handler
	Service *svcs.Service
	Module  module.Module
}

// WireAssetTree creates the Asset Tree handler, service, and module
func WireAssetTree(cfg *config.Config, log *zap.Logger, coreDeps *CoreDeps) (*AssetTreeWiring, error) {
	repo, err := repos.NewRepository(coreDeps.AssetsDB.DB, log)
	if err != nil {
		return nil, err
	}

	service := svcs.NewService(repo, log)
	handler := assettree.NewHandler(service, log)
	mod := module.NewAssetTreeModule(cfg, log, handler)

	return &AssetTreeWiring{
		Handler: handler,
		Service: service,
		Module:  mod,
	}, nil
}
