package buildbundle

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/lifecycle"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assetindex"
	"go.uber.org/zap"
)

// AssetLifecycleDeps is the dependency bag for the asset lifecycle service.
type AssetLifecycleDeps struct {
	Registry      artifacts.Registry
	Publisher     delivery.Publisher
	AssetIndex    *assetindex.Service
	DriveVerifier artifacts.DriveVerifier
	DriveReader   drive.Reader
	Finalizer     *artifacts.Finalizer
	Store         lifecycle.AssetRecordStore
}

// NewLifecycleFromDeps creates a lifecycle Service using the provided dependencies.
func NewLifecycleFromDeps(deps *AssetLifecycleDeps, log *zap.Logger) *lifecycle.Service {
	driveReader := deps.DriveReader
	if deps.DriveVerifier == nil && driveReader != nil {
		deps.DriveVerifier = drive.NewDriveVerifierAdapter(driveReader)
	}
	if deps.Finalizer == nil && deps.Registry != nil && deps.DriveVerifier != nil && deps.AssetIndex != nil {
		deps.Finalizer = artifacts.NewFinalizerWithPorts(
			deps.Registry,
			deps.DriveVerifier,
			newArtifactAssetIndexAdapter(deps.AssetIndex),
			semantic.NewArtifactsMetadataAdapter(),
			log,
		)
	}
	if deps.Store == nil && deps.Registry != nil {
		deps.Store = lifecycle.NewRegistryStoreAdapter(deps.Registry)
	}
	return lifecycle.NewService(lifecycle.ServiceDeps{
		Store:       deps.Store,
		Publisher:   deps.Publisher,
		DriveReader: driveReader,
		Registry:    deps.Registry,
		AssetIndex:  deps.AssetIndex,
		Finalizer:   deps.Finalizer,
		Log:         log,
	}, lifecycle.DefaultConfig())
}
