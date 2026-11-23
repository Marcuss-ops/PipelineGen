package script

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase/gencore"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/require"
)

func mixedSegmentPlanFixture() *scriptpkg.ResolvedGenerationPlan {
	return &scriptpkg.ResolvedGenerationPlan{
		ID:         "mixed-segments",
		SourceKind: string(scriptpkg.SourceClips),
		Mode:       "clip_to_script",
		SourceText: "global clip evidence",
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"clip-1", "clip-2"},
			ClipCount:       2,
			ClipDetails: map[string]scriptpkg.ClipDetail{
				"clip-1": {Name: "first clip"},
				"clip-2": {Name: "second clip"},
			},
			SegmentEvidence: []scriptpkg.SegmentClipEvidence{
				{SegmentID: "vo-01", Topic: "Narration", SourceText: "narration brief"},
				{SegmentID: "cut-01", Topic: "Cutaway", SourceText: "cutaway brief", ClipIDs: []string{"clip-1"}, Clips: map[string]scriptpkg.ClipDetail{
					"clip-1": {Name: "first clip"},
				}},
				{SegmentID: "cut-02", Topic: "Second cutaway", SourceText: "second cutaway brief", ClipIDs: []string{"clip-2"}, Clips: map[string]scriptpkg.ClipDetail{
					"clip-2": {Name: "second clip"},
				}},
			},
		},
	}
}

func TestScopeSegmentPlanTurnsNarrationOnlySegmentIntoTextPlan(t *testing.T) {
	plan := mixedSegmentPlanFixture()
	got := scopeSegmentPlan(plan, scriptpkg.ScriptSegment{
		ID:         "vo-01",
		Topic:      "Narration",
		SourceText: "narration brief",
	}, 0)

	require.Nil(t, got.ClipEvidence)
	require.Equal(t, string(scriptpkg.SourceText), got.SourceKind)
	require.Equal(t, scriptpkg.ModeForSource(scriptpkg.SourceText), got.Mode)
	require.Equal(t, "narration brief", got.SourceText)
	modelSource, err := gencore.ModelSourceText(&got)
	require.NoError(t, err, "a narration-only segment must not fail the clip-native evidence gate")
	require.Equal(t, "narration brief", modelSource)
}

func TestScopeSegmentPlanKeepsOnlyOwnedClipEvidenceForCutaway(t *testing.T) {
	plan := mixedSegmentPlanFixture()
	got := scopeSegmentPlan(plan, scriptpkg.ScriptSegment{
		ID:         "cut-02",
		Topic:      "Second cutaway",
		SourceText: "second cutaway brief",
		ClipIDs:    []string{"clip-2"},
	}, 1) // deliberately positional mismatch: ID lookup must win

	require.NotNil(t, got.ClipEvidence)
	require.Equal(t, []string{"clip-2"}, got.ClipEvidence.AcceptedClipIDs)
	require.Len(t, got.ClipEvidence.SegmentEvidence, 1)
	require.Equal(t, "cut-02", got.ClipEvidence.SegmentEvidence[0].SegmentID)
	require.Equal(t, []string{"clip-2"}, got.ClipEvidence.SegmentEvidence[0].ClipIDs)
	require.Equal(t, "second clip", got.ClipEvidence.SegmentEvidence[0].Clips["clip-2"].Name)
}
