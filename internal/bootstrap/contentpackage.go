package bootstrap

import (
	"go.uber.org/zap"
	"velox/go-master/internal/service/contentpackage"
)

// ContentPackageWiring holds the content package module wiring
type ContentPackageWiring struct {
	Service *contentpackage.Service
}

// WireContentPackage creates the content package service and registers its job handler
func WireContentPackage(
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*ContentPackageWiring, error) {
	svc := contentpackage.NewService(log, nil, coreDeps.JobsService)

	// Register job handler with the job service
	if coreDeps.JobsService != nil {
		svc.RegisterHandler(coreDeps.JobsService)
	}

	return &ContentPackageWiring{
		Service: svc,
	}, nil
}
