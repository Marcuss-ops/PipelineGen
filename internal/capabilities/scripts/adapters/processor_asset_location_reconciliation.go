// Package adapters — processor_asset_location_reconciliation.go
// verifies every drive_link in the SpecScene bindings against the
// canonical AssetLocationVerifier before the document is published.
//
// The processor runs after clip_bindings and stock_bindings have
// populated the per-scene bindings and before the document and
// persistence processors consume them. It ensures:
//
//   - No broken link reaches the Google Doc renderer.
//   - Stale links are replaced with the canonical webViewLink.
//   - Missing/trashed/inaccessible links are cleared and flagged
//     as warnings.
//
// The processor is BestEffort: transport errors are surfaced as
// warnings rather than hard failures, but every unverified link is
// cleared before downstream publication. A nil verifier is still a
// hard composition error.
//
// File layout:
//   - this file: type, constructors, Name, Policy
//   - reconciliation_process.go: the per-scene verification loop
//     (Process) plus the pure helpers it uses
//   - reconciliation_verify.go: the single-link verifier wrapper
//     (verifyAndReconcile) and its result type
package adapters

import (
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// AssetLocationReconciliationProcessor verifies every drive_link
// in the SpecScene bindings and reconciles them against the
// canonical AssetLocationVerifier.
type AssetLocationReconciliationProcessor struct {
	verifier  scriptpkg.AssetLocationVerifier
	committer scriptpkg.AssetLocationCommitter
}

// NewAssetLocationReconciliationProcessor creates the processor.
// verifier must be non-nil (enforced at registration time).
func NewAssetLocationReconciliationProcessor(
	verifier scriptpkg.AssetLocationVerifier,
) *AssetLocationReconciliationProcessor {
	return &AssetLocationReconciliationProcessor{verifier: verifier}
}

// NewDurableAssetLocationReconciliationProcessor adds the canonical
// SQLite/outbox commit port. Keeping this as a separate constructor makes
// the durable dependency explicit and prevents silently ignoring a second
// committer at composition time.
func NewDurableAssetLocationReconciliationProcessor(
	verifier scriptpkg.AssetLocationVerifier,
	committer scriptpkg.AssetLocationCommitter,
) *AssetLocationReconciliationProcessor {
	return &AssetLocationReconciliationProcessor{verifier: verifier, committer: committer}
}

func (p *AssetLocationReconciliationProcessor) Name() ProcessorName {
	return ProcessorAssetLocationReconciliation
}

// Policy is BestEffort for verification-only composition. A configured
// committer makes the complete reconciliation Required because the
// downstream SpecScene must not be published when its durable asset
// mutation and Qdrant outbox event could not be committed.
func (p *AssetLocationReconciliationProcessor) Policy(
	_ *scriptpkg.ResolvedGenerationPlan,
) ProcessorPolicy {
	if p == nil || p.verifier == nil || p.committer != nil {
		return ProcessorRequired
	}
	return ProcessorBestEffort
}
