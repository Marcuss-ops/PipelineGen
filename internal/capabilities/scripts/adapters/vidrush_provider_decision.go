package adapters

import (
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// SegmentProviderDecision describes eligible providers in priority order for
// one segment. Eligibility is always intersected with the hard ProviderPolicy.
type SegmentProviderDecision struct {
	SegmentID      string               `json:"segment_id"`
	Preferences    []ProviderPreference `json:"preferences,omitempty"`
	Strategy       string               `json:"strategy,omitempty"`
	Model          string               `json:"model,omitempty"`
	Version        string               `json:"version,omitempty"`
	CandidateLimit int                  `json:"candidate_limit,omitempty"`
}

func buildSegmentProviderDecision(plan *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult, contentType string) SegmentProviderDecision {
	decision := SegmentProviderDecision{SegmentID: segment.SegmentID}
	if plan == nil {
		return decision
	}
	decision.Strategy = strings.TrimSpace(plan.MediaPlan.Planner.Strategy)
	decision.Model = strings.TrimSpace(plan.MediaPlan.Planner.Model)
	decision.Version = strings.TrimSpace(plan.MediaPlan.Planner.Version)
	decision.CandidateLimit = plan.MediaPlan.Planner.CandidateLimit
	selection, err := NewVidRushProviderSelector().Select(plan, segment, contentType)
	if err != nil {
		return decision
	}
	decision.Preferences = append([]ProviderPreference(nil), selection.Preferences...)
	return decision
}

func providerDecisionAllows(decision SegmentProviderDecision, provider string) bool {
	if len(decision.Preferences) == 0 {
		return false
	}
	for _, preference := range decision.Preferences {
		if preference.Provider == provider {
			return true
		}
	}
	return false
}

func providerEnabledByHardPolicy(plan *scriptpkg.ResolvedGenerationPlan, provider string) bool {
	if plan == nil {
		return false
	}
	return providerAllowed(plan.MediaPlan.ProviderPolicy, provider)
}

func effectiveProviderEnabled(plan *scriptpkg.ResolvedGenerationPlan, decision SegmentProviderDecision, provider string) bool {
	return providerEnabledByHardPolicy(plan, provider) && providerDecisionAllows(decision, provider)
}
