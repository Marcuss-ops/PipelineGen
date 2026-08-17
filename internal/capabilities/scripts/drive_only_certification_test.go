// Package scriptgeneration — drive_only_certification_test.go is the
// Drive-only clip certification battery for the "46 Love clip" regression:
//
//	Evidence / Script / Timeline
//	        ≠
//	Binary Materialization / Render
//
// GenerateTimeline=true compiles the canonical timeline from Drive-only clip
// references (transcript ready, no local path, no binary SHA) without staging
// any MP4 and without enqueuing a render job. PipelineGen is audio-only: the
// run stops at the certified final_audio.m4a and never requires local media
// for a video render.
package scriptgeneration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// driveOnlyScenesFor builds one scene per Drive-only clip (transcript ready,
// Drive link present, no local path, no binary SHA). Each clip keeps its own
// 1:1 scene — planning must never merge clips.
func driveOnlyScenesFor(clips []*ClipReference) []Scene {
	scenes := make([]Scene, 0, len(clips))
	for i, clip := range clips {
		startMS := clip.SourceInMS
		durMS := clip.SourceOutMS - clip.SourceInMS
		intent := capabilityaudio.AudioIntent{
			Mode: capabilityaudio.AudioClip, ClipAssetID: clip.ID,
			SourceInUS: startMS * 1000, SourceDurationUS: durMS * 1000,
			TimelineOffsetUS: 0, TimelineDurationUS: durMS * 1000, UseOriginalAudio: true,
		}
		scenes = append(scenes, Scene{
			ID:           fmt.Sprintf("scene-%d", i),
			Index:        i,
			DurationMS:   durMS,
			DurationUS:   durMS * 1000,
			Clip:         clip,
			Text:         map[Language]string{"en": fmt.Sprintf("Scene %d over a drive-only clip.", i)},
			Audio:        intent,
			AudioIntents: []capabilityaudio.AudioIntent{intent},
		})
	}
	return scenes
}

func driveOnlyClip(id string, seconds int64) *ClipReference {
	return &ClipReference{
		ID:          id,
		Title:       "Drive-only clip " + id,
		DriveLink:   "https://drive.google.com/file/d/" + id,
		Duration:    float64(seconds),
		SourceInMS:  0,
		SourceOutMS: seconds * 1000,
	}
}

// TestGenerateTimeline_DriveOnlyClipPreservesCanonicalBinding certifies that
// 1 clip = 1 scene = 1 timeline segment: the canonical binding (asset_id)
// survives planning. The timeline compiles metadata, not a render payload.
func TestGenerateTimeline_DriveOnlyClipPreservesCanonicalBinding(t *testing.T) {
	runner, repo, textGen, _, _, _, _ := newTestRunner()
	textGen.scenes = driveOnlyScenesFor([]*ClipReference{driveOnlyClip("clip-drive-1", 45), driveOnlyClip("clip-drive-2", 12)})

	req := defaultTestRequest()
	req.GenerateTimeline = true
	req.Docs = DocumentsConfig{Enabled: false}

	runID := "run-timeline-binding-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status, "timeline run must complete: %s", final.ErrorMessage)
	require.NotNil(t, final.Result.CanonicalTimeline, "GenerateTimeline=true must compile a canonical timeline")
	require.Len(t, final.Result.CanonicalTimeline.Segments, 2)

	assert.Equal(t, "clip-drive-1", final.Result.CanonicalTimeline.Segments[0].Video.AssetID, "scene 0 must keep clip 0")
	assert.Equal(t, "clip-drive-2", final.Result.CanonicalTimeline.Segments[1].Video.AssetID, "scene 1 must keep clip 1")
}

// TestRegression_GenerateTimelineDriveOnlyClipDoesNotFailWithAssetHasNoLocalPath	// is the permanent regression guard for the original failure: 46 Love clips,
// all with transcript ready, failed with "asset has no local path" because
// output.generate_timeline implicitly demanded materialized binaries.
// Drive-only clips with ready transcripts must complete with a canonical
// timeline and zero render work.
func TestRegression_GenerateTimelineDriveOnlyClipDoesNotFailWithAssetHasNoLocalPath(t *testing.T) {
	runner, repo, textGen, _, _, _, renderEnq := newTestRunner()
	textGen.scenes = driveOnlyScenesFor([]*ClipReference{
		driveOnlyClip("clip-love-001", 45),
		driveOnlyClip("clip-love-002", 38),
		driveOnlyClip("clip-love-003", 52),
	})

	req := defaultTestRequest()
	req.GenerateTimeline = true
	req.Docs = DocumentsConfig{Enabled: false}

	runID := "run-regression-46-love-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status, "drive-only timeline must not fail: %s", final.ErrorMessage)
	require.NotNil(t, final.Result.CanonicalTimeline)
	require.Len(t, final.Result.CanonicalTimeline.Segments, 3)

	assert.NotContains(t, final.ErrorMessage, "asset has no local path")
	assert.NotContains(t, final.ErrorMessage, "binary sha256 required")
	assert.Equal(t, 0, renderEnq.callCount, "timeline-only run must never enqueue a render job")
}
