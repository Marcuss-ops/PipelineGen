// Package app — WireAssets storage capability builder (PR-WIRE-ASSETS-CAPABILITY-SPLIT, July 2026).
//
// The storage capability is unique in the assets tree today: exposure
// of a raw Handler alongside the Module is needed because
// RegisterInternalMediaRoutes is a Handler-level method (not a
// Module-level one). Mirrors the clips precedent exactly.
//
// godlike/06 SSOT: this file is the canonical owner of the storage
// build pipeline. The canonical storage handler lives in
// internal/capabilities/assets/storage/; this file is composition-root glue only.
//
// PR-WIRE-ASSETS-NIL-CLASSIFICATION (2026-07-25): the descriptor
// type-assertion goes through ClassifyDepGet (DepRequired, production
// fail-closed).
package wiring

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/catalogsync"
	assetstorage "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/storage"
	"go.uber.org/zap"
)
import (
	assetsdiag "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/diagnostics"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
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
//	(b) the AssetsWiring.InternalMediaHandler forwarder
//	    (storageDesc.Handler) — the QDRANT-001 closure kept a narrow
//	    api.MediaInternalRouter port at internal/platform/httpserver/routes.go::Setup().
//	    The Router binds this via Router.SetInternalMediaHandler,
//	    which calls storageDesc.Handler.RegisterInternalMediaRoutes(...)
//	    for the /internal/v1/media/sync server-to-server surface.
//
// storage is always on in production (no feature flag) and has no
// per-feature middleware.
func buildStorageBundle(
	log *zap.Logger,
	jobs *JobsBundle,
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

// buildDiagnosticsBundle constructs the canonical *assetsdiag.DiagnosticsDescriptor.
//
// The function returns BOTH the descriptor AND the underlying
// *assetsdiag.Service so the caller (WireAssets) can emit a contextual
// log line ("diagnostics and search services wired with production
// ports" vs "diagnostics service NOT fully wired — some routes will
// return 503") based on whether the service was constructed (clipsRepo
// was non-nil).
//
// QDRANT-005 Fase 1 (June 2026): diagIndexHealthAdapter is nil-tolerant
// for the qdrant dep — when ClipsRepo is nil the handler falls back to
// 503. PR 3 (June 2026): real SQLite + Qdrant deps.
//
// Blocco C1-Step 10 (June 2026): the diagnostics capability is now
// built via the canonical diagnostics.Build(deps) (api.Descriptor,
// error) contract. The Handler is constructed inside Build and
// captured by the returned DiagnosticsDescriptor's Module closure.
// The composition site type-asserts ONCE to
// *assetsdiag.DiagnosticsDescriptor (fail-closed) and reuses the
// concrete for the assetsapi.NewModule(..., Diagnostics: dd, ...) call
// (the concrete *DiagnosticsDescriptor satisfies api.Descriptor
// structurally). The diagnostics capability has no non-HTTP consumer
// in the codebase (the 3 routes are the entire public surface,
// reachable only via HTTP), so the Descriptor surface is the smallest
// possible — just `Module` field + forwarder methods (matches the
// stock / voiceover / soundeffect / register precedent exactly).
func buildDiagnosticsBundle(
	log *zap.Logger,
	clipsRepo *sqassets.ClipsRepository,
) (*assetsdiag.DiagnosticsDescriptor, *assetsdiag.Service, error) {
	// (1) Build the application-layer *assetsdiag.Service from typed-port
	// adapters. Nil-tolerant: when clipsRepo is nil the handler returns
	// 503 (per QDRANT-005 Fase 1 + Blocco C1-Step 10 + AGENTS.md
	// Pattern 0 — the api/ layer never builds the service directly).
	var diagSvc *assetsdiag.Service
	if clipsRepo != nil {
		diagSvc = assetsdiag.NewService(
			&diagIndexHealthAdapter{clips: clipsRepo, qdrant: nil, collectionName: ""},
			&diagAssetStatsAdapter{clips: clipsRepo},
			&zapDiagLogAdapter{log: log},
		)
	}

	// (2) Canonical Build call
	descriptor, err := assetsdiag.Build(assetsdiag.Dependencies{
		Service:     diagSvc,
		EnabledFunc: func() bool { return true }, // diagnostics is always on in production
		ModuleOpts:  nil,                         // no per-feature middleware (matches pre-Step-10 wiring)
		Logger:      log,
	})
	if err != nil {
		return nil, nil, err
	}

	// (3) Type-assert to the concrete *DiagnosticsDescriptor
	desc, ok := descriptor.(*assetsdiag.DiagnosticsDescriptor)
	if err := ClassifyDepGet(fmt.Sprintf("WireAssets: diagnostics (got %T, want *assetsdiag.DiagnosticsDescriptor)", descriptor), !ok || desc == nil, DepRequired, log); err != nil {
		return nil, nil, err
	}
	return desc, diagSvc, nil
}
