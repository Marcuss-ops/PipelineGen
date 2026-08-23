// Package usecase — quality_gate_coverage.go
//
// Coverage rules of the editorial quality gate: source_text coverage
// against the policy threshold and clip_evidence binding coverage.
package usecase

import scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

// sourceCoverageChecker fails when the source_text coverage is below the
// policy threshold (only enforced when the source was evaluated — a
// source-free generation is NOT_EVALUATED and cannot fail this rule).
type sourceCoverageChecker struct{}

func (sourceCoverageChecker) Name() string { return "source_text_coverage" }

func (sourceCoverageChecker) Check(in qualityGateInput) []string {
	if in.q.SourceTextCoverageStatus == "EVALUATED" && in.q.SourceTextCoverage < in.minSourceCov {
		return []string{"source_text coverage below threshold"}
	}
	return nil
}

// clipCoverageChecker fails when the clip_evidence binding coverage is
// below the policy threshold. The threshold is zeroed by the orchestrator
// when no clip evidence exists, so this rule only bites clip-backed plans.
type clipCoverageChecker struct{}

func (clipCoverageChecker) Name() string { return "clip_evidence_coverage" }

func (clipCoverageChecker) Check(in qualityGateInput) []string {
	if in.q.ClipEvidenceCoverage < in.minClipCov {
		policyLabel := in.plan.GroundingPolicy
		if policyLabel == "" {
			policyLabel = "default"
		}
		return []string{"clip_evidence coverage below threshold for " + policyLabel}
	}
	return nil
}

// computeClipEvidenceCoverage returns the ratio of accepted clips that
// are bound to a scene in the result. For non-clip sources it returns
// 1.0.
func computeClipEvidenceCoverage(result *scriptpkg.GenerationResult, plan scriptpkg.ResolvedGenerationPlan) float64 {
	if plan.ClipEvidence == nil || len(plan.ClipEvidence.AcceptedClipIDs) == 0 {
		return 1.0
	}
	accepted := plan.ClipEvidence.AcceptedClipIDs
	if plan.NumClips > 0 && plan.NumClips < len(accepted) {
		accepted = accepted[:plan.NumClips]
	}
	if len(accepted) == 0 {
		return 1.0
	}
	bound := make(map[string]struct{})
	for _, s := range result.Output.SpecScene.Scenes {
		if s.Bindings.Clip != nil && s.Bindings.Clip.ClipID != "" {
			bound[s.Bindings.Clip.ClipID] = struct{}{}
		}
	}
	matches := 0
	for _, id := range accepted {
		if _, ok := bound[id]; ok {
			matches++
		}
	}
	return float64(matches) / float64(len(accepted))
}
