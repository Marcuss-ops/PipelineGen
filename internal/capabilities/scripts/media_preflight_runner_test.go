package scriptgeneration

import (
	"context"
	"errors"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/require"
)

type failingMediaPreflight struct {
	result PreflightResult
	calls  int
}

func (p *failingMediaPreflight) Run(context.Context, GenerateRequest) PreflightResult {
	p.calls++
	return p.result
}

func TestRunnerMediaPreflightFailsBeforeTextGeneration(t *testing.T) {
	runner, repo, textGen, _, _, _, _ := newTestRunner()
	preflight := &failingMediaPreflight{result: PreflightResult{
		Failures: []PreflightFailure{{
			Category: "fixed_clip_audio",
			AssetID:  "intro-1",
			Detail:   "authoritative original audio unavailable",
		}},
	}}
	runner.SetMediaPreflight(preflight)

	req := defaultTestRequest()
	req.Docs = DocumentsConfig{}
	req.DocsEnabled = false
	req.Languages = nil
	req.Audio = "none"
	req.Intro = &scriptpkg.FixedSection{
		ClipIDs: []string{"intro-1"},
		Playback: scriptpkg.FixedPlaybackPolicy{
			AudioMode:   scriptpkg.FixedPlaybackOriginalClip,
			SourceInMS:  0,
			SourceOutMS: 1_000,
		},
	}
	runID := "run-preflight-before-generation"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)

	final, err := repo.Get(context.Background(), runID)
	require.NoError(t, err)
	require.NotNil(t, final)
	require.Equal(t, RunStatusFailed, final.Status)
	require.Equal(t, StagePreflight, final.FailedStage)
	require.Equal(t, "MEDIA_PREFLIGHT_FAILED", final.ErrorCode)
	require.Equal(t, 1, preflight.calls)
	require.Equal(t, 0, textGen.callCount, "LLM must not run before media preflight passes")

	err = preflight.result.AsError()
	var typed *MediaPreflightError
	require.True(t, errors.As(err, &typed))
	require.True(t, errors.Is(err, ErrMediaPreflight))
}
