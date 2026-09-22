package scriptgeneration

import (
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRecordRenderStageProgressCountsTheRealFanOut pins the unit of the render
// stage: one render per (scene, clip, language). A scene fanned out across
// languages reports one observation per language, not one per run.
func TestRecordRenderStageProgressCountsTheRealFanOut(t *testing.T) {
	result := &GenerateResult{}
	recordRenderStageProgress(result, LocalizedRenderResult{SceneID: "scene-1", Language: "it", ClipID: "clip-7"})
	recordRenderStageProgress(result, LocalizedRenderResult{SceneID: "scene-1", Language: "en", ClipID: "clip-7"})

	progress := result.StageProgress[string(job.StageRender)]
	require.Len(t, progress.Languages, 2, "one render observation per language")
	assert.Equal(t, 2, progress.Total)
	assert.Equal(t, 2, progress.Completed)
	assert.Equal(t, job.StageRender, progress.Stage)
}

// TestRecordRenderStageProgressUpsertsTheSameUnit pins the identity: the ready
// path and the published path both see the same render, so recording it twice
// converges instead of inflating the parent's fan-out count.
func TestRecordRenderStageProgressUpsertsTheSameUnit(t *testing.T) {
	result := &GenerateResult{}
	rendered := LocalizedRenderResult{SceneID: "scene-1", Language: "it", ClipID: "clip-7", Status: "RENDERED"}
	recordRenderStageProgress(result, rendered)
	recordRenderStageProgress(result, rendered)

	progress := result.StageProgress[string(job.StageRender)]
	assert.Equal(t, 1, progress.Total)
	assert.Equal(t, 1, progress.Completed)
}

// TestRecordRenderStageProgressKeepsClipsOfOneSceneApart pins that the clip is
// part of the unit: a fixed intro/outro renders several source clips under one
// scene, and collapsing them would under-report the fan-out.
func TestRecordRenderStageProgressKeepsClipsOfOneSceneApart(t *testing.T) {
	result := &GenerateResult{}
	recordRenderStageProgress(result, LocalizedRenderResult{SceneID: "scene-intro", Language: "it", ClipID: "intro-a"})
	recordRenderStageProgress(result, LocalizedRenderResult{SceneID: "scene-intro", Language: "it", ClipID: "intro-b"})

	progress := result.StageProgress[string(job.StageRender)]
	require.Len(t, progress.Languages, 2)
	assert.Equal(t, "scene-intro/intro-a", progress.Languages[0].Unit)
	assert.Equal(t, "scene-intro/intro-b", progress.Languages[1].Unit)
}

// TestRecordRenderStageFailureIsAttributedToItsUnit pins that a failed render
// fails only its own unit: the other renders of the run stay completed and the
// parent can see which one failed and why.
func TestRecordRenderStageFailureIsAttributedToItsUnit(t *testing.T) {
	result := &GenerateResult{}
	recordRenderStageProgress(result, LocalizedRenderResult{SceneID: "scene-1", Language: "it", ClipID: "clip-7"})
	recordRenderStageFailure(result, LocalizedRenderFailure{
		SceneID: "scene-2", Language: "it", ClipID: "clip-9", ErrorCode: "RENDER_FAILED", Error: "chronon exited 1",
	})

	progress := result.StageProgress[string(job.StageRender)]
	require.Len(t, progress.Languages, 2)
	assert.Equal(t, 2, progress.Total)
	assert.Equal(t, 1, progress.Completed)
	for _, observation := range progress.Languages {
		if observation.Unit == "scene-2/clip-9" {
			assert.Equal(t, job.StageFailed, observation.Status)
			assert.Equal(t, "chronon exited 1", observation.Error)
		}
	}
}

// TestRecordRenderStageProgressToleratesAMissingResult pins the nil contract:
// the streaming fan-out calls the recorder with a nil result on the paths where
// the runner has not adopted one yet, and that must be a no-op, never a panic.
func TestRecordRenderStageProgressToleratesAMissingResult(t *testing.T) {
	recordRenderStageProgress(nil, LocalizedRenderResult{SceneID: "scene-1", Language: "it", ClipID: "clip-7"})
	recordRenderStageFailure(nil, LocalizedRenderFailure{SceneID: "scene-1", Language: "it", ErrorCode: "X"})
}

func TestRenderUnitKey(t *testing.T) {
	assert.Equal(t, "scene-1/clip-7", renderUnitKey("scene-1", "clip-7"))
	assert.Equal(t, "scene-1", renderUnitKey("scene-1", ""))
	assert.Equal(t, "clip-7", renderUnitKey("", "clip-7"))
	assert.Equal(t, "", renderUnitKey("  ", "  "))
}
