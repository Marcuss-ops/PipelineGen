package usecase

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestResearchCandidateCoverageCheckerFailsWhenCandidateIsOmitted(t *testing.T) {
	checker := researchCandidateCoverageChecker{}
	got := checker.Check(qualityGateInput{
		result: &scriptpkg.GenerationResult{Output: scriptpkg.ScriptOutput{Text: "Cristiano Ronaldo e Lionel Messi hanno grandi guadagni."}},
		plan: scriptpkg.ResolvedGenerationPlan{ResearchEvidence: &scriptpkg.ResearchEvidencePack{
			Candidates: []scriptpkg.RankedResearchCandidate{
				{Label: "Cristiano Ronaldo"},
				{Label: "Canelo Álvarez"},
			},
		}},
	})
	if len(got) != 1 || got[0] == "" {
		t.Fatalf("expected omitted-candidate failure, got %v", got)
	}
}

func TestResearchCandidateCoverageCheckerNormalizesAccents(t *testing.T) {
	got := (researchCandidateCoverageChecker{}).Check(qualityGateInput{
		result: &scriptpkg.GenerationResult{Output: scriptpkg.ScriptOutput{Text: "Canelo Alvarez ha costruito una grande carriera."}},
		plan: scriptpkg.ResolvedGenerationPlan{ResearchEvidence: &scriptpkg.ResearchEvidencePack{
			Candidates: []scriptpkg.RankedResearchCandidate{{Label: "Canelo Álvarez"}},
		}},
	})
	if len(got) != 0 {
		t.Fatalf("accent-normalized candidate should pass, got %v", got)
	}
}
