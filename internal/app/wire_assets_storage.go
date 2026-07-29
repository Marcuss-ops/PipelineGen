// Package app — WireAssets storage capability builder (PR-WIRE-ASSETS-CAPABILITY-SPLIT, July 2026).
//
// The storage capability is unique in the assets tree today: exposure
// of a raw Handler alongside the Module is needed because
// RegisterInternalMediaRoutes is a Handler-level method (not a
// Module-level one). Mirrors the clips precedent exactly.
//
// godlike/06 SSOT: this file is the canonical owner of the storage
// build pipeline. The canonical storage handler lives in
// internal/api/assets/storage/; this file is composition-root glue only.
//
// PR-WIRE-ASSETS-NIL-CLASSIFICATION (2026-07-25): the descriptor
// type-assertion goes through ClassifyDepGet (DepRequired, production
// fail-closed).
package app

import (
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"fmt"

	assetstorage "github.com/Marcuss-ops/PipelineGen/internal/api/assets/storage"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/catalogsync"
	"go.uber.org/zap"
)

// buildStorageBundle constructs the canonical *assetstorage.StorageDescriptor.
//
// Blocco C1-Step 12 (June 2026; user-documented Step 11): the Handler
// is constructed inside Build and captured by the returned
// StorageDescriptor's Module closure. The composition site
// type-asserts ONCE to *assetstorage.StorageDescriptor (fail-closed)
// and reuses the concrete for both consumers:
//
//	(a) the assetsapi.NewModule(..., Storage: storageDesc, ...) call
//	    (the concrete *StorageDescriptor satisfies api.Descriptor
//	    structurally — its RegisterRoutes forwarder delegates to the
//	    embedded api.Module which mounts POST /api/media/sync on the
//	    parent /api/media group); AND
//	(b) the wiring.AssetsWiring.InternalMediaHandler forwarder
//	    (storageDesc.Handler) — the QDRANT-001 closure kept a narrow
//	    api.MediaInternalRouter port at internal/api/routes.go::Setup().
//	    The Router binds this via Router.SetInternalMediaHandler,
//	    which calls storageDesc.Handler.RegisterInternalMediaRoutes(...)
//	    for the /internal/v1/media/sync server-to-server surface.
//
// storage is always on in production (no feature flag) and has no
// per-feature middleware.
func buildStorageBundle(
	log *zap.Logger,
	jobs *wiring.JobsBundle,
	catalogSync *catalogsync.Service,
) (*assetstorage.StorageDescriptor, error) {
	descriptor, err := assetstorage.Build(assetstorage.Dependencies{
		Jobs:        jobs.Facade,
		CatalogSync: catalogSync,
		EnabledFunc: func() bool { return true }, // storage is always on in production
		ModuleOpts:  nil,                         // no per-feature middleware (matches pre-Step-12 wiring)
		Logger:      log,
	})
	if err != nil {
		return nil, err
	}
	desc, ok := descriptor.(*assetstorage.StorageDescriptor)
	if err := ClassifyDepGet(fmt.Sprintf("WireAssets: storage (got %T, want *assetstorage.StorageDescriptor)", descriptor), !ok || desc == nil, DepRequired, log); err != nil {
		return nil, err
	}
	return desc, nil
}
