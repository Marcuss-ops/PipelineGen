package usecase

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestModelSourceTextUsesResearchEvidenceOverStalePlanText(t *testing.T) {
	pack := &scriptpkg.ResearchEvidencePack{
		Version: scriptpkg.ResearchEvidenceVersion,
		Topic:   "richest boxers",
		Candidates: []scriptpkg.RankedResearchCandidate{{
			CandidateID: "mayweather", Label: "Floyd Mayweather Jr.", Rank: 1,
			Sources: []scriptpkg.ResearchWebSource{{ID: "mayweather:S1", URL: "https://example.com/mayweather"}},
			Claims:  []scriptpkg.ResearchClaim{{Text: "Documented career earnings.", Verified: true, SourceIDs: []string{"mayweather:S1"}}},
		}},
	}

	modelText, err := modelSourceText(&scriptpkg.ResolvedGenerationPlan{
		ID: "research-plan", SourceKind: string(scriptpkg.SourceResearch),
		SourceText: "STALE/WRONG TEXT", ResearchEvidence: pack,
	})
	require.NoError(t, err)
	require.NotContains(t, modelText, "STALE/WRONG TEXT")
	require.Contains(t, modelText, "RANK 1")
	require.Contains(t, modelText, "Floyd Mayweather Jr.")
}

func TestModelSourceTextRejectsResearchWithoutEvidence(t *testing.T) {
	_, err := modelSourceText(&scriptpkg.ResolvedGenerationPlan{
		ID: "research-plan", SourceKind: string(scriptpkg.SourceResearch), SourceText: "STALE/WRONG TEXT",
	})
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "research plan requires research evidence"))
}
