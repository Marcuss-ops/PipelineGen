package script

import "context"

// QualityGateEvaluator is the injectable editorial quality-gate boundary.
// The default implementation remains the Go qualitygate package; the Rust
// semantic-engine adapter implements this interface for the opt-in cutover.
//
// It lives in the kernel so a platform adapter never has to import a
// capability/usecase package to satisfy the contract.
type QualityGateEvaluator interface {
	EvaluateQualityGate(
		ctx context.Context,
		result *GenerationResult,
		item GenerationItemV2,
		plan ResolvedGenerationPlan,
	) (*GenerationQuality, error)
}
