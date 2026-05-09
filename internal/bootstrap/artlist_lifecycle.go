package bootstrap

import (
	"go.uber.org/zap"
	"velox/go-master/internal/core/lifecycle"
	"velox/go-master/internal/service/mediaregistry"
)

func wireArtlistLifecycle(coreDeps *CoreDeps, log *zap.Logger) *lifecycle.Service {
	clipsRegistry := mediaregistry.NewClipsRegistry(coreDeps.ArtlistRepo)
	return NewLifecycleFromDeps(&LifecycleDeps{
		Registry:    clipsRegistry,
		DriveClient: coreDeps.DriveClient,
		AssetIndex:  coreDeps.AssetIndexService,
	}, log)
}
