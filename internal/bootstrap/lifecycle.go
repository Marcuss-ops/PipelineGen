package bootstrap

import (
	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"

	"velox/go-master/internal/core/lifecycle"
	"velox/go-master/internal/service/assetindex"
	"velox/go-master/internal/service/mediaregistry"
)

// LifecycleDeps holds the dependencies needed to create a lifecycle service
type LifecycleDeps struct {
	Registry      mediaregistry.Registry
	DriveClient   *gdrive.Service
	AssetIndex    *assetindex.Service
	DriveVerifier mediaregistry.DriveVerifier
	Finalizer     *mediaregistry.Finalizer
	Store         lifecycle.AssetRecordStore
}

// NewLifecycleFromDeps creates a lifecycle Service using the provided dependencies.
// This eliminates the boilerplate of creating verifier, finalizer, store adapter, and lifecycle.
func NewLifecycleFromDeps(
	deps *LifecycleDeps,
	log *zap.Logger,
) *lifecycle.Service {
	// Create drive verifier if not provided
	if deps.DriveVerifier == nil && deps.DriveClient != nil {
		deps.DriveVerifier = mediaregistry.NewAPIDriveVerifier(deps.DriveClient)
	}

	// Create finalizer if not provided
	if deps.Finalizer == nil && deps.Registry != nil && deps.DriveVerifier != nil && deps.AssetIndex != nil {
		deps.Finalizer = mediaregistry.NewFinalizerWithAssetIndex(
			deps.Registry,
			deps.DriveVerifier,
			deps.AssetIndex,
			log,
		)
	}

	// Create store adapter if not provided
	if deps.Store == nil && deps.Registry != nil {
		deps.Store = lifecycle.NewRegistryStoreAdapter(deps.Registry)
	}

	// Create and return lifecycle service
	return lifecycle.NewService(
		deps.Store,
		deps.DriveClient,
		deps.Registry,
		deps.AssetIndex,
		deps.Finalizer,
		lifecycle.DefaultConfig(),
		log,
	)
}
