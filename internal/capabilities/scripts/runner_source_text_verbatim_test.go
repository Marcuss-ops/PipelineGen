package scriptgeneration

import (
	"context"
	"errors"
	"testing"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestRunner_SourceTextVerbatim_PreservesSceneIdentityAndSkipsGenerator(t *testing.T) {
	repo := newInMemRunRepository()
	textGen := newStubTextGenerator(nil)
	textGen.err = errors.New("text generator must not be called for verbatim source text")
	runner := NewRunner(repo, textGen, newStubTranslator(), nil, nil)
	runner.SetLogger(zap.NewNop())

	req := GenerateRequest{
		IdempotencyKey: "verbatim-source-text",
		Source:         Source{Type: SourceText, Topic: "existing script"},
		SourceLanguage: "en",
		Languages:      []Language{"en", "it"},
		Audio:          capabilityaudio.AudioModeNone,
		ScriptParams: scriptpkg.ScriptSpec{
			SourceTextVerbatim: true,
			Segments: []scriptpkg.ScriptSegment{
				{ID: "scene-one", Topic: "first", SourceText: "Exact first scene."},
				{ID: "scene-two", Topic: "second", SourceText: "Exact second scene."},
			},
		},
	}
	runID := "run-verbatim-source-text"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status)
	require.Equal(t, 0, textGen.callCount)
	require.Len(t, final.Result.Scenes, 2)
	require.Equal(t, "scene-one", final.Result.Scenes[0].ID)
	require.Equal(t, "Exact first scene.", final.Result.Scenes[0].Text["en"])
	require.Equal(t, "scene-two", final.Result.Scenes[1].ID)
	require.Equal(t, "Exact second scene.", final.Result.Scenes[1].Text["en"])
	require.NotEmpty(t, final.Result.Scenes[0].Text["it"])
	require.NotEmpty(t, final.Result.Scenes[1].Text["it"])
}

func TestMaterializeVerbatimSourceTextScenesRejectsInvalidInput(t *testing.T) {
	base := GenerateRequest{
		Source:         Source{Type: SourceText},
		SourceLanguage: "en",
		ScriptParams: scriptpkg.ScriptSpec{Segments: []scriptpkg.ScriptSegment{{
			ID: "scene-one", Topic: "first", SourceText: "Exact first scene.",
		}}},
	}
	if _, err := materializeVerbatimSourceTextScenes(base); err != nil {
		t.Fatalf("valid request: %v", err)
	}

	cases := []struct {
		name string
		edit func(*GenerateRequest)
	}{
		{"missing_text", func(r *GenerateRequest) { r.ScriptParams.Segments[0].SourceText = " " }},
		{"duplicate_id", func(r *GenerateRequest) {
			r.ScriptParams.Segments = append(r.ScriptParams.Segments, r.ScriptParams.Segments[0])
		}},
		{"non_text_source", func(r *GenerateRequest) { r.Source.Type = SourceClips }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := base
			req.ScriptParams.Segments = append([]scriptpkg.ScriptSegment(nil), base.ScriptParams.Segments...)
			tc.edit(&req)
			if _, err := materializeVerbatimSourceTextScenes(req); err == nil {
				t.Fatal("expected invalid verbatim source text to fail")
			}
		})
	}
}
