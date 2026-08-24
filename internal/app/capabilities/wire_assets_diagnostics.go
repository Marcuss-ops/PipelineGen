// Package app — WireAssets diagnostics capability builder (PR-WIRE-ASSETS-CAPABILITY-SPLIT, July 2026).
//
// The diagnostics capability needs only 1 business dep (clipsRepo) +
// constructs the appdiag.Service from typed-port adapters.
//
// godlike/06 SSOT: this file is the canonical owner of the diagnostics
// build pipeline. The canonical diagnostics handler lives in
// internal/api/assets/diagnostics/; this file is composition-root glue only.
//
// PR-WIRE-ASSETS-NIL-CLASSIFICATION (2026-07-25): the descriptor
// type-assertion goes through ClassifyDepGet (DepRequired, production
// fail-closed).
package capabilities

import (
	"fmt"

	assetsdiag "github.com/Marcuss-ops/PipelineGen/internal/api/assets/diagnostics"
	appdiag "github.com/Marcuss-ops/PipelineGen/internal/application/assets/diagnostics"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets"
	"go.uber.org/zap"
)

// buildDiagnosticsBundle constructs the canonical *assetsdiag.DiagnosticsDescriptor.
//
// The function returns BOTH the descriptor AND the underlying
// *appdiag.Service so the caller (WireAssets) can emit a contextual
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
) (*assetsdiag.DiagnosticsDescriptor, *appdiag.Service, error) {
	// (1) Build the application-layer *appdiag.Service from typed-port
	// adapters. Nil-tolerant: when clipsRepo is nil the handler returns
	// 503 (per QDRANT-005 Fase 1 + Blocco C1-Step 10 + AGENTS.md
	// Pattern 0 — the api/ layer never builds the service directly).
	var diagSvc *appdiag.Service
	if clipsRepo != nil {
		diagSvc = appdiag.NewService(
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
