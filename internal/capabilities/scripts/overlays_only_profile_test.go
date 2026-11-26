package scriptgeneration

import (
	"context"
	"encoding/json"
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	"github.com/stretchr/testify/require"
)

// TestOverlaysOnlyProjectionDeclaresCanonicalV3PayloadProfile pins the request
// boundary that used to leave overlays-only runs without an output profile:
// RenderingGen then published native bytes and skipped the canonical V3 pass.
// An overlays-only run renders the same opaque H.264 video-only artifacts as a
// final job, so its plan must carry the V3 profile AND the no-alpha contract
// derived from it, in the plan payload itself.
func TestOverlaysOnlyProjectionDeclaresCanonicalV3PayloadProfile(t *testing.T) {
	scenes := overlayPlanBranchScenes()
	for i := range scenes {
		scenes[i].Voiceover = map[Language]AudioReference{
			"en": {ID: "vo-" + scenes[i].ID, FilePath: "/tmp/vo.mp3", Duration: 5},
		}
		// No timing artifact in this fixture: the short-phrase lane is the
		// explicit opt-in that lets the planner anchor these windows without
		// pretending the fixture carries certified word timing.
		scenes[i].ShortPhraseOverlay = true
		scenes[i].DurationMS = 5_000
	}
	req := defaultTestRequest()
	req.Languages = []Language{"en"}
	req.Render.OverlaysOnly = true
	req.FinalJob = false

	runner := projectionTestRunner()
	runner.SetOverlayCanvas(OverlayCanvasSpec{Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1})
	result := &GenerateResult{SourceLanguage: "en", Scenes: scenes}
	planning := newOverlayPlanningInput("run-overlays-only-v3", "", req.Project, result)
	projected, err := runner.computeOverlayPlanBranchProjection(
		context.Background(), req, capabilityaudio.AudioModeChunkedVoiceover, planning)
	require.NoError(t, err)
	require.NotNil(t, projected, "the overlays-only projection must produce a document")
	result = projected
	require.NotNil(t, result.OverlayPlan, "the overlays-only projection must produce a plan")

	plan := result.OverlayPlan
	require.Equal(t, kernelmedia.OverlayVideoOnlyProfileID, plan.OutputProfileID,
		"overlays-only plans must request the canonical V3 profile")
	require.Equal(t, capmodel.DefaultOverlayContractNoAlpha.ID, plan.MediaContract,
		"the V3 profile must derive the no-alpha media contract")

	raw, err := json.Marshal(plan)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"output_profile_id":"`+kernelmedia.OverlayVideoOnlyProfileID+`"`,
		"the serialized plan payload must carry output_profile_id V3")
}

// TestIntermediateOverlayProjectionKeepsAlphaProfile pins the other half of the
// same mapping: an overlay that is not a final job and not overlays-only is an
// intermediate alpha artifact, so it must not inherit the video-only profile.
func TestIntermediateOverlayProjectionKeepsAlphaProfile(t *testing.T) {
	if got := overlayOutputProfileID(GenerateRequest{}); got != "" {
		t.Fatalf("intermediate overlay profile = %q, want empty (alpha default)", got)
	}
}
