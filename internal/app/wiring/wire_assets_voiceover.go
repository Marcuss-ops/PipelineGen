// Package app — WireAssets voiceover capability builder (PR-WIRE-ASSETS-CAPABILITY-SPLIT, July 2026).
//
// The voiceover capability needs only JobsSvc — the canonical
// /generate route binds GenerateVoiceoversCommand + enqueues via
// jobsSvc. GroupsResolver + VoiceoverRootFolder are no longer
// referenced at this module layer (the legacy /groups +
// /generate-with-group /batch /promo /sync routes have all been
// retired per Wave 21 / PR-VOICEOVER-RECOVERY V1..V7; see
// architecture/deprecations.yaml PR-VO-SUNSET-MACHINERY-RETIRE
// for the tracked closure of the 2026-06-28 → 2026-09-26 Sunset window).
//
// godlike/06 SSOT: this file is the canonical owner of the voiceover
// build pipeline. The canonical voiceover handler lives in
// internal/capabilities/assets/voiceover/; this file is composition-root glue only.
//
// PR-WIRE-ASSETS-NIL-CLASSIFICATION (2026-07-25): the descriptor
// type-assertion goes through ClassifyDepGet (DepRequired, production
// fail-closed).
package wiring

import (
	"fmt"

	assetvoice "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/voiceover"
	"go.uber.org/zap"
)

// buildVoiceoverBundle constructs the canonical *assetvoice.VoiceoverDescriptor.
//
// Blocco C1-Step 7 (June 2026): the Handler is constructed inside
// Build and captured by the returned VoiceoverDescriptor's Module
// closure. The composition site type-asserts ONCE to
// *assetvoice.VoiceoverDescriptor (fail-closed) and reuses the
// concrete for the assetsapi.NewModule(..., Voiceover: vd, ...) call
// (the concrete *VoiceoverDescriptor satisfies api.Descriptor
// structurally). The voiceover capability has no non-HTTP consumer in
// the codebase (/generate is the entire public surface, reachable only
// via HTTP), so the Descriptor surface is the smallest possible — just
// `Module` field + forwarder methods (matches the stock precedent
// exactly).
//
// voiceover is always on in production (no feature flag) and has no
// per-feature middleware.
func buildVoiceoverBundle(
	log *zap.Logger,
	jobs *JobsBundle,
) (*assetvoice.VoiceoverDescriptor, error) {
	descriptor, err := assetvoice.Build(assetvoice.Dependencies{
		Jobs:        jobs.Facade,
		EnabledFunc: func() bool { return true }, // voiceover is always on in production (no feature flag)
		ModuleOpts:  nil,                         // no per-feature middleware for the voiceover capability (matches pre-Step-7 wiring)
		Logger:      log,
	})
	if err != nil {
		return nil, err
	}
	desc, ok := descriptor.(*assetvoice.VoiceoverDescriptor)
	if err := ClassifyDepGet(fmt.Sprintf("WireAssets: voiceover (got %T, want *assetvoice.VoiceoverDescriptor)", descriptor), !ok || desc == nil, DepRequired, log); err != nil {
		return nil, err
	}
	return desc, nil
}
