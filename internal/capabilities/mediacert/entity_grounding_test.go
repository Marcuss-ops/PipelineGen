package mediacert

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/sceneir"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/require"
)

// Extraction surface (September 2026): VisualNER mines the committed
// NARRATION — the per-segment SourceText is an editorial brief for text and
// clips payloads. An entity demonstrated verbatim in NarrationText is
// therefore first-class evidence: NO EVIDENCE → NO ENTITY still holds
// because the entity is shown in the text the video actually speaks, the
// same surface the entity timeline anchors overlay cards to.

// TestRuleEntityGroundingAcceptsNarrationGroundedEntity pins the positive
// side of the contract: an entity present only in the narration (the brief
// does not contain it) must pass the grounding gate.
func TestRuleEntityGroundingAcceptsNarrationGroundedEntity(t *testing.T) {
	const brief = "In 1990, the champion lost his heavyweight title in Tokyo."
	const narration = "Mike Tyson walked away from Tokyo after the upset."
	seg := ResultSegment{
		SegmentID:      "tokyo-douglas",
		SourceText:     brief,
		SourceTextHash: script.ComputeCanonicalSegmentTextHash(brief),
		NarrationText:  narration,
		Insights: script.SegmentInsights{
			Entities: []script.ExtractedEntity{
				{Value: "Mike Tyson", Type: "PERSON", Confidence: 0.95},
			},
		},
	}

	check := ruleEntityGrounding(Spec{}, MediaResult{Segments: []ResultSegment{seg}})
	require.True(t, check.Passed,
		"narration-grounded entity must pass ruleEntityGrounding: %+v", check.Violations)
	require.Empty(t, check.Violations)
}

// TestRuleEntityGroundingStillRejectsUngroundedEntity pins the negative
// side: narration evidence widens the haystack but never disables the
// rule — an entity absent from both source and narration is still rejected
// with the NO EVIDENCE → NO ENTITY violation.
func TestRuleEntityGroundingStillRejectsUngroundedEntity(t *testing.T) {
	const brief = "In 1990, the champion lost his heavyweight title in Tokyo."
	const narration = "The champion faced a career-defining defeat on a global stage."
	seg := ResultSegment{
		SegmentID:      "tokyo-douglas",
		SourceText:     brief,
		SourceTextHash: script.ComputeCanonicalSegmentTextHash(brief),
		NarrationText:  narration,
		Insights: script.SegmentInsights{
			Entities: []script.ExtractedEntity{
				{Value: "Elon Musk", Type: "PERSON", Confidence: 0.95},
			},
		},
	}

	check := ruleEntityGrounding(Spec{}, MediaResult{Segments: []ResultSegment{seg}})
	require.False(t, check.Passed, "ungrounded entity must still be rejected")
	require.Len(t, check.Violations, 1)
	require.Contains(t, check.Violations[0].Detail, "NO EVIDENCE → NO ENTITY")
	require.Contains(t, check.Violations[0].Detail, "Elon Musk")
}

// TestRuleEntityGroundingReadsSceneIRNarrationWhenPresent pins the SceneIR
// branch: when the compiled SceneIR is attached it wins over the flat
// fields, and its NarrationText is consulted alongside its SourceText.
func TestRuleEntityGroundingReadsSceneIRNarrationWhenPresent(t *testing.T) {
	const irSource = "The brief never names the public figure."
	const irNarration = "The narrator says Elon Musk built his first company as a teenager."
	ir := sceneir.SceneIR{
		SegmentID:      "early-internet",
		SourceText:     irSource,
		SourceTextHash: script.ComputeCanonicalSegmentTextHash(irSource),
		NarrationText:  irNarration,
	}
	grounded := ResultSegment{
		SegmentID: "early-internet",
		// Deliberately stale flat fields: the SceneIR branch must win.
		SourceText:    "stale flat source",
		NarrationText: "stale flat narration",
		SceneIR:       &ir,
		Insights: script.SegmentInsights{
			Entities: []script.ExtractedEntity{
				{Value: "Elon Musk", Type: "PERSON", Confidence: 0.9},
			},
		},
	}
	check := ruleEntityGrounding(Spec{}, MediaResult{Segments: []ResultSegment{grounded}})
	require.True(t, check.Passed,
		"SceneIR.NarrationText must count as evidence: %+v", check.Violations)

	ungrounded := ResultSegment{
		SegmentID: "early-internet",
		SceneIR:   &ir,
		Insights: script.SegmentInsights{
			Entities: []script.ExtractedEntity{
				{Value: "Nikola Tesla", Type: "PERSON", Confidence: 0.9},
			},
		},
	}
	check = ruleEntityGrounding(Spec{}, MediaResult{Segments: []ResultSegment{ungrounded}})
	require.False(t, check.Passed,
		"entity absent from SceneIR source AND narration must be rejected")
	require.Len(t, check.Violations, 1)
	require.Contains(t, check.Violations[0].Detail, "Nikola Tesla")
}
