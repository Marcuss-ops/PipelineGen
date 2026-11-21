package scriptgeneration

import (
	"context"
	"strings"
	"testing"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/require"
)

// ORCH-07 gate 2 — end-to-end coverage of the streaming path with durable
// stock bindings. The race this ticket tracked (R8) was that the scene-ready
// coordinator ran before applyDurableStockBindings, saw Stock == nil on scenes
// the caller had bound, and launched Chronon renders the assembler discarded;
// the run then failed with INCOMPLETE_RENDER_SET (expected=3 successful=6).
//
// The unit tests pin the projection in isolation. This test walks the real
// path — streaming generator → emit → per-scene binding → coordinator →
// localized render fan-out → final render set — and asserts the exact
// contract: only unbound scenes reach Chronon, and the run completes with
// expected == successful.
func TestORCH07_StreamingStockBindingsRenderOnlyUnboundScenes(t *testing.T) {
	runner, repo, _, _, _, _ := newTestRunner()
	enq := &recordingLocalizedRenderEnqueuer{producedVideo: &LocalizedRenderResult{
		AssetID: "rendered-asset", SHA256: strings.Repeat("a", 64),
		DriveFileID: "drive-1", DriveLink: "https://drive.test/rendered",
		DurationMS: 12_000, Status: "UPLOADED",
	}}
	runner.SetLocalizedRenderEnqueuer(enq)
	runner.SetMediaPreflight(passThroughMediaPreflight{})
	runner.SetTTSConcurrency(1)
	runner.SetTranslationConcurrency(1)

	// Every generated scene owns a source clip, so the only reason a scene can
	// be skipped is its stock binding. The generator streams the three scenes
	// (release is closed, so it never gates); streaming is what makes this an
	// ORCH-07 test rather than a batch-path test.
	scenes := defaultTestScenes()
	for i := range scenes {
		scenes[i].Clip = &ClipReference{ID: "clip-source", SHA256: strings.Repeat("b", 64), DurationUS: 12_000_000}
		scenes[i].Text["en"] = "This narrated scene explains the important background and context behind the selected story in detail."
	}
	streamer := newGatedStreamingTextGenerator(scenes)
	close(streamer.release)
	runner.textGen = streamer

	req := defaultTestRequest()
	// The R8 shape: a fixed-media opening clip plus stock-bound body scenes.
	// The fixed intro is a real render unit; the stock scenes are not.
	req.Intro = &scriptpkg.FixedSection{ClipIDs: []string{"intro-clip"}, DisplayText: "Welcome"}
	req.Render.Subtitles = &scriptpkg.VideoSubtitlesSpec{Enabled: true}
	req.Render.Enabled = true
	req.Source.Type = SourceClips
	req.Source.ClipIDs = []string{"clip-source", "clip-source", "clip-source"}
	req.Languages = []Language{"en"}
	req.VoiceoverLanguages = nil
	req.MediaMode = scriptpkg.MediaModeMixed
	req.StockBindings = []scriptpkg.StockBindingInput{
		{Index: 0, FolderID: "folder-a", StartMs: 0, EndMs: 5000},
		{Index: 1, FolderID: "folder-b", StartMs: 0, EndMs: 4000},
	}

	runID := "run-orch07-streaming-stock"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status, final.ErrorMessage)
	require.NotNil(t, final.Result)

	// The two stock-bound scenes must never be enqueued: their video comes from
	// the binding, and a Chronon render would be duplicated work the assembler
	// has to throw away.
	dispatched := enq.snapshot()
	require.Len(t, dispatched, 2, "only the fixed intro and the unbound scene may reach Chronon: %+v", sceneIDs(dispatched))
	dispatchedByScene := map[string]int{}
	for _, in := range dispatched {
		dispatchedByScene[in.SceneID]++
	}
	require.Equal(t, 1, dispatchedByScene["scene-intro"], "the fixed opening clip is one render unit")
	require.Equal(t, 1, dispatchedByScene["scene-2"], "the unbound body scene is one render unit")
	require.NotContains(t, dispatchedByScene, "scene-0", "stock-bound scene must never reach Chronon")
	require.NotContains(t, dispatchedByScene, "scene-1", "stock-bound scene must never reach Chronon")

	// The exact render-set contract is what failed live in R8. Completion above
	// already proves INCOMPLETE_RENDER_SET did not fire; this pins the numbers.
	require.Equal(t, 2, final.Result.ExpectedRenderCount)
	require.Len(t, final.Result.LocalizedRenders, 2)
	require.Empty(t, final.Result.LocalizedRenderFailures)
	require.True(t, renderSetSatisfiesExpectation(
		final.Result.ExpectedRenderCount, len(final.Result.LocalizedRenders), len(final.Result.LocalizedRenderFailures)))

	// The stock scenes keep their binding into the durable result, so the final
	// assembly still has the media the coordinator skipped.
	stockScenes := 0
	for _, scene := range final.Result.Scenes {
		if scene.Stock != nil {
			stockScenes++
		}
	}
	require.Equal(t, 2, stockScenes, "both bound scenes must carry Stock into the result")
}

func sceneIDs(inputs []LocalizedRenderInput) []string {
	ids := make([]string, 0, len(inputs))
	for _, in := range inputs {
		ids = append(ids, in.SceneID)
	}
	return ids
}
