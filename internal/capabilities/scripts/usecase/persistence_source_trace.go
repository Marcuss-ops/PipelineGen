package usecase

import scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

func buildGenerationSourceTrace(plan scriptpkg.ResolvedGenerationPlan, engineResult *EngineResult) scriptpkg.SourceTrace {
	var trace scriptpkg.SourceTrace
	if engineResult.ClipEvidence != nil {
		clipIDs := engineResult.ClipEvidence.AcceptedClipIDs
		if plan.NumClips > 0 && plan.NumClips < len(clipIDs) {
			clipIDs = clipIDs[:plan.NumClips]
		}
		trace.AcceptedClipIDs = append([]string(nil), clipIDs...)
	}
	if len(engineResult.SearchResults) > 0 {
		trace.SearchResults = append([]scriptpkg.SearchResultItem(nil), engineResult.SearchResults...)
	}
	if plan.ResearchEvidence != nil {
		trace.ResearchEvidence = plan.ResearchEvidence.Clone()
		if plan.ResearchReport != nil {
			report := *plan.ResearchReport
			report.Evidence = plan.ResearchEvidence.Clone()
			trace.ResearchReport = &report
		} else {
			trace.ResearchReport = &scriptpkg.ResearchReport{
				Status: "SUCCEEDED", Mode: "multi_candidate", SearchEnabled: true,
				Searched: true, QualityGatePassed: true, Evidence: plan.ResearchEvidence.Clone(),
			}
		}
	}
	return trace
}
